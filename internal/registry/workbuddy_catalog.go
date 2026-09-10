package registry

import "strings"

// WorkBuddy international catalog captured from www.workbuddy.ai GET /v3/config
// (2026-09-10). Live fetches overlay the same fields from data.models[].
// Do not copy Codex/OpenAI models.json windows or thinking levels onto WorkBuddy IDs.

type workBuddyCatalogCaps struct {
	displayName         string
	contextLength       int
	maxCompletionTokens int
	levels              []string
	defaultLevel        string
	zeroAllowed         bool
	noThinking          bool
}

var workBuddyGlobalCatalog = map[string]workBuddyCatalogCaps{
	"default-model":    {displayName: "Auto", contextLength: 176000, maxCompletionTokens: 24000, noThinking: true},
	"fast-model":       {displayName: "Fast", contextLength: 200000, maxCompletionTokens: 32000, defaultLevel: "medium"},
	"balanced-model":   {displayName: "Balanced", contextLength: 256000, maxCompletionTokens: 32000, defaultLevel: "medium"},
	"primary-model":    {displayName: "Primary", contextLength: 272000, maxCompletionTokens: 72000, defaultLevel: "high"},
	"deep-model":       {displayName: "Deep", contextLength: 176000, maxCompletionTokens: 24000, noThinking: true},
	"gpt-6-astra":      {displayName: "GPT-6-Astra", contextLength: 1000000, maxCompletionTokens: 128000, levels: []string{"low", "medium", "high", "xhigh", "max"}, defaultLevel: "high", zeroAllowed: true},
	"hy4-preview":      {displayName: "Hy4 preview", contextLength: 1000000, maxCompletionTokens: 64000, levels: []string{"high"}, defaultLevel: "high"},
	"hy3":              {displayName: "Hy3", contextLength: 192000, maxCompletionTokens: 64000, levels: []string{"low", "high"}, defaultLevel: "high"},
	"gpt-5.6-sol":      {displayName: "GPT-5.6-Sol", contextLength: 1000000, maxCompletionTokens: 128000, levels: []string{"low", "medium", "high", "xhigh", "max"}, defaultLevel: "high", zeroAllowed: true},
	"gpt-5.6-terra":    {displayName: "GPT-5.6-Terra", contextLength: 1000000, maxCompletionTokens: 128000, levels: []string{"low", "medium", "high", "xhigh", "max"}, defaultLevel: "high", zeroAllowed: true},
	"gpt-5.6-luna":     {displayName: "GPT-5.6-Luna", contextLength: 1000000, maxCompletionTokens: 128000, levels: []string{"low", "medium", "high", "xhigh", "max"}, defaultLevel: "high", zeroAllowed: true},
	"gpt-5.5":          {displayName: "GPT-5.5", contextLength: 1000000, maxCompletionTokens: 128000, levels: []string{"low", "medium", "high", "xhigh"}, defaultLevel: "high"},
	"gpt-5.4":          {displayName: "GPT-5.4", contextLength: 272000, maxCompletionTokens: 72000, levels: []string{"low", "medium", "high", "xhigh"}, defaultLevel: "high"},
	"gpt-5.3-codex":    {displayName: "GPT-5.3-Codex", contextLength: 272000, maxCompletionTokens: 72000, defaultLevel: "medium"},
	"gemini-3.5-flash": {displayName: "Gemini-3.5-Flash", contextLength: 1000000, maxCompletionTokens: 65536, defaultLevel: "medium"},
	"glm-5.3":          {displayName: "GLM-5.3", contextLength: 1000000, maxCompletionTokens: 48000, levels: []string{"low", "high", "max"}, defaultLevel: "high", zeroAllowed: true},
	"glm-5.2":          {displayName: "GLM-5.2", contextLength: 1000000, maxCompletionTokens: 48000, levels: []string{"high", "xhigh"}, defaultLevel: "high", zeroAllowed: true},
	"kimi-k3":          {displayName: "Kimi-K3", contextLength: 1000000, maxCompletionTokens: 32000, defaultLevel: "medium"},
	"kimi-k2.6":        {displayName: "Kimi-K2.6", contextLength: 256000, maxCompletionTokens: 32000, defaultLevel: "medium"},
}

func (c workBuddyCatalogCaps) thinking() *ThinkingSupport {
	if c.noThinking {
		return nil
	}
	levels := append([]string(nil), c.levels...)
	def := strings.ToLower(strings.TrimSpace(c.defaultLevel))
	if len(levels) == 0 && def != "" {
		levels = []string{def}
	}
	if len(levels) == 0 && def == "" {
		return nil
	}
	return &ThinkingSupport{
		Levels:       levels,
		DefaultLevel: def,
		ZeroAllowed:  c.zeroAllowed,
	}
}

func (c workBuddyCatalogCaps) apply(model *ModelInfo) {
	if model == nil {
		return
	}
	if c.displayName != "" {
		model.DisplayName = c.displayName
		model.Description = c.displayName + " via WorkBuddy"
	}
	if c.contextLength > 0 {
		model.ContextLength = c.contextLength
	}
	if c.maxCompletionTokens > 0 {
		model.MaxCompletionTokens = c.maxCompletionTokens
	}
	if c.noThinking {
		model.Thinking = nil
		return
	}
	if thinking := c.thinking(); thinking != nil {
		model.Thinking = thinking
	}
}

// WorkBuddyLiveMeta is the subset of GET /v3/config data.models[] used at runtime.
type WorkBuddyLiveMeta struct {
	DisplayName         string
	ContextLength       int
	MaxCompletionTokens int
	DefaultLevel        string
	Levels              []string
	ZeroAllowed         bool
	HasReasoning        bool
	ClearThinking       bool
}

// ApplyWorkBuddyLiveMeta overlays live /v3/config model fields. Live windows and
// reasoning replace the bundled catalog for that ID.
func ApplyWorkBuddyLiveMeta(model *ModelInfo, meta WorkBuddyLiveMeta) {
	if model == nil {
		return
	}
	if name := strings.TrimSpace(meta.DisplayName); name != "" {
		model.DisplayName = name
		model.Description = name + " via WorkBuddy"
	}
	if meta.ContextLength > 0 {
		model.ContextLength = meta.ContextLength
	}
	if meta.MaxCompletionTokens > 0 {
		model.MaxCompletionTokens = meta.MaxCompletionTokens
	}
	if meta.ClearThinking {
		model.Thinking = nil
		model.UserDefined = false
		return
	}
	if !meta.HasReasoning {
		return
	}
	levels := normalizeWorkBuddyEfforts(meta.Levels)
	def := strings.ToLower(strings.TrimSpace(meta.DefaultLevel))
	if len(levels) == 0 && def != "" {
		levels = []string{def}
	}
	model.Thinking = &ThinkingSupport{
		Levels:       levels,
		DefaultLevel: def,
		ZeroAllowed:  meta.ZeroAllowed,
	}
	model.UserDefined = false
}

func normalizeWorkBuddyEfforts(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
