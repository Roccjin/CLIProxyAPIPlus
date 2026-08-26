package executor

import (
	"encoding/json"
	"sort"
	"strings"

	qoderauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qoder"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/tidwall/gjson"
)

var qoderStandardContextSizes = []string{"200K", "400K", "1M"}

var qoderThinkingOrder = []string{"low", "medium", "high", "max", "xhigh"}

// QoderCatalogModel is the management-facing row for per-model Qoder defaults.
type QoderCatalogModel struct {
	Key            string   `json:"key"`
	DisplayName    string   `json:"display_name,omitempty"`
	ThinkingLevels []string `json:"thinking_levels,omitempty"`
	ZeroAllowed    bool     `json:"zero_allowed,omitempty"`
	ContextSizes   []string `json:"context_sizes,omitempty"`
	CatalogContext string   `json:"catalog_context,omitempty"`
}

func parseQoderModelRequest(raw string) (key, thinkingSuffix, contextSize string) {
	raw = strings.TrimSpace(raw)
	contextSize, raw = stripQoderContextBracket(raw)
	suffix := thinking.ParseSuffix(raw)
	base := suffix.ModelName
	if suffix.HasSuffix {
		thinkingSuffix = strings.TrimSpace(suffix.RawSuffix)
	}
	extraCtx, base := stripQoderContextBracket(base)
	if contextSize == "" {
		contextSize = extraCtx
	}
	key = strings.TrimPrefix(base, "qoder/")
	return key, thinkingSuffix, contextSize
}

func stripQoderContextBracket(model string) (label, rest string) {
	trimmed := strings.TrimSpace(model)
	if len(trimmed) < 4 {
		return "", trimmed
	}
	if strings.HasSuffix(strings.ToLower(trimmed), "[1m]") {
		return "1M", strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	return "", trimmed
}

func lookupQoderModelDefault(cfg *config.Config, modelKey string) (thinkingLevel, contextSize string) {
	if cfg == nil || len(cfg.QoderModelDefaults) == 0 {
		return "", ""
	}
	key := strings.TrimPrefix(strings.TrimSpace(modelKey), "qoder/")
	if d, ok := cfg.QoderModelDefaults[key]; ok {
		return d.Thinking, d.Context
	}
	return "", ""
}

func resolveQoderThinking(chatReq map[string]interface{}, suffix, defaultThinking string) (enable *bool, effort string) {
	raw := ""
	if strings.TrimSpace(suffix) != "" {
		raw = suffix
	} else if v, ok := stringField(chatReq, "reasoning_effort"); ok {
		raw = v
	} else if v, ok := stringField(chatReq, "thinking"); ok {
		raw = v
	} else {
		raw = defaultThinking
	}
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return nil, ""
	}
	switch raw {
	case "off", "none", "disabled", "false", "0":
		off := false
		return &off, ""
	case "on", "enabled", "true":
		on := true
		return &on, ""
	default:
		on := true
		return &on, raw
	}
}

func resolveQoderContextTokens(chatReq map[string]interface{}, suffixLabel, defaultLabel string) int {
	label := suffixLabel
	if label == "" {
		if v, ok := stringField(chatReq, "context_size"); ok {
			label = v
		}
	}
	if label == "" {
		label = defaultLabel
	}
	return qoderContextTokens(label)
}

func qoderContextTokens(size string) int {
	switch strings.ToUpper(strings.TrimSpace(size)) {
	case "200K", "200000":
		return 200000
	case "400K", "400000":
		return 400000
	case "1M", "1000000":
		return 1000000
	}
	return 0
}

func stringField(obj map[string]interface{}, key string) (string, bool) {
	if obj == nil {
		return "", false
	}
	v, ok := obj[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

func applyQoderRuntimeKnobs(reqBody, modelConfig map[string]interface{}, enable *bool, effort string, contextTokens int) {
	if reqBody == nil {
		return
	}
	params, _ := reqBody["parameters"].(map[string]interface{})
	if params == nil {
		params = map[string]interface{}{}
		reqBody["parameters"] = params
	}
	if contextTokens > 0 {
		params["context_length"] = float64(contextTokens)
		if modelConfig != nil {
			modelConfig["max_input_tokens"] = float64(contextTokens)
		}
	}
	if enable == nil {
		return
	}
	on := *enable
	params["enable_thinking"] = on
	if modelConfig != nil {
		modelConfig["is_reasoning"] = on
	}
	if extra := qoderChatContextModelConfig(reqBody); extra != nil {
		extra["is_reasoning"] = on
	}
	if on {
		if effort != "" && effort != "on" && effort != "enabled" {
			params["reasoning_effort"] = effort
		}
		return
	}
	delete(params, "reasoning_effort")
}

func qoderChatContextModelConfig(reqBody map[string]interface{}) map[string]interface{} {
	ctx, _ := reqBody["chat_context"].(map[string]interface{})
	if ctx == nil {
		return nil
	}
	extra, _ := ctx["extra"].(map[string]interface{})
	if extra == nil {
		return nil
	}
	mc, _ := extra["modelConfig"].(map[string]interface{})
	return mc
}

func isQoderAuthExpiredMessage(message string) bool {
	return strings.Contains(message, `"code":"105"`) || strings.Contains(message, "Login expired")
}

func isQoderUsageUnauthorized(status int) bool {
	return status == 401 || status == 403
}

// CollectQoderCatalog builds management catalog rows from a PAT/OAuth model cache.
func CollectQoderCatalog(storage *qoderauth.QoderTokenStorage) []QoderCatalogModel {
	if storage == nil {
		return nil
	}
	keys := storage.ModelConfigKeys()
	sort.Strings(keys)
	out := make([]QoderCatalogModel, 0, len(keys))
	for _, key := range keys {
		raw, ok := storage.GetModelConfig(key)
		if !ok || len(raw) == 0 {
			continue
		}
		if model, ok := ParseQoderCatalogEntry(raw); ok {
			out = append(out, model)
		}
	}
	return out
}

// FallbackQoderCatalog returns the static ModelMap keys with standard context sizes
// so the management UI can still set defaults before a live model list is cached.
func FallbackQoderCatalog() []QoderCatalogModel {
	keys := make([]string, 0, len(qoderauth.ModelMap))
	for key := range qoderauth.ModelMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]QoderCatalogModel, 0, len(keys))
	for _, key := range keys {
		out = append(out, QoderCatalogModel{
			Key:            key,
			DisplayName:    key,
			ThinkingLevels: append([]string{}, qoderThinkingOrder...),
			ZeroAllowed:    true,
			ContextSizes:   append([]string{}, qoderStandardContextSizes...),
		})
	}
	return out
}

// ParseQoderCatalogEntry reads thinking/context options from a cached model_config blob.
func ParseQoderCatalogEntry(raw json.RawMessage) (QoderCatalogModel, bool) {
	key := strings.TrimSpace(gjson.GetBytes(raw, "key").String())
	if key == "" {
		return QoderCatalogModel{}, false
	}
	display := strings.TrimSpace(gjson.GetBytes(raw, "display_name").String())
	if display == "" {
		display = key
	}
	sizes, catalogDefault := parseQoderContextConfig(gjson.GetBytes(raw, "context_config"))
	levels, zeroAllowed := parseQoderThinkingConfig(gjson.GetBytes(raw, "thinking_config"))
	return QoderCatalogModel{
		Key:            key,
		DisplayName:    display,
		ThinkingLevels: levels,
		ZeroAllowed:    zeroAllowed,
		ContextSizes:   sizes,
		CatalogContext: catalogDefault,
	}, true
}

func parseQoderContextConfig(cc gjson.Result) (sizes []string, catalogDefault string) {
	found := make(map[string]struct{}, len(qoderStandardContextSizes)+4)
	if cc.Exists() && cc.IsObject() {
		cc.ForEach(func(k, v gjson.Result) bool {
			label := normalizeQoderContextLabel(k.String())
			if label == "" {
				return true
			}
			found[label] = struct{}{}
			if v.Get("is_default").Bool() && catalogDefault == "" {
				catalogDefault = label
			}
			return true
		})
	}
	for _, size := range qoderStandardContextSizes {
		found[size] = struct{}{}
	}
	sizes = make([]string, 0, len(found))
	for _, size := range qoderStandardContextSizes {
		if _, ok := found[size]; ok {
			sizes = append(sizes, size)
			delete(found, size)
		}
	}
	if len(found) == 0 {
		return sizes, catalogDefault
	}
	extra := make([]string, 0, len(found))
	for size := range found {
		extra = append(extra, size)
	}
	sort.Strings(extra)
	return append(sizes, extra...), catalogDefault
}

func parseQoderThinkingConfig(tc gjson.Result) (levels []string, zeroAllowed bool) {
	if !tc.Exists() {
		return nil, false
	}
	if tc.Get("disabled").Exists() {
		zeroAllowed = true
	}
	efforts := tc.Get("enabled.efforts")
	found := map[string]struct{}{}
	if efforts.Exists() && efforts.IsObject() {
		efforts.ForEach(func(k, _ gjson.Result) bool {
			level := strings.ToLower(strings.TrimSpace(k.String()))
			if level != "" {
				found[level] = struct{}{}
			}
			return true
		})
	}
	for _, level := range qoderThinkingOrder {
		if _, ok := found[level]; ok {
			levels = append(levels, level)
			delete(found, level)
		}
	}
	if len(found) == 0 {
		return levels, zeroAllowed
	}
	extra := make([]string, 0, len(found))
	for level := range found {
		extra = append(extra, level)
	}
	sort.Strings(extra)
	return append(levels, extra...), zeroAllowed
}

func normalizeQoderContextLabel(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "200K", "200000":
		return "200K"
	case "400K", "400000":
		return "400K"
	case "1M", "1000000":
		return "1M"
	default:
		return strings.TrimSpace(raw)
	}
}
