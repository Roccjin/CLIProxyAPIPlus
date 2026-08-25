package executor

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
)

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
