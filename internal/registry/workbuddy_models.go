package registry

import "strings"

// GetWorkBuddyModels returns the available models for WorkBuddy (Tencent).
// These models are served through the www.workbuddy.cn API.
func GetWorkBuddyModels() []*ModelInfo {
	now := int64(1748044800) // 2025-05-24
	return []*ModelInfo{
		{
			ID:                  "auto",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "Auto",
			Description:         "Automatic model selection via WorkBuddy",
			ContextLength:       128000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-5v-turbo",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "GLM-5v Turbo",
			Description:         "GLM-5v Turbo via WorkBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-5.1",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "GLM-5.1",
			Description:         "GLM-5.1 via WorkBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-5.0-turbo",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "GLM-5.0 Turbo",
			Description:         "GLM-5.0 Turbo via WorkBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-5.0",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "GLM-5.0",
			Description:         "GLM-5.0 via WorkBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-4.7",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "GLM-4.7",
			Description:         "GLM-4.7 via WorkBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "minimax-m2.7",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "MiniMax M2.7",
			Description:         "MiniMax M2.7 via WorkBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "minimax-m2.5",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "MiniMax M2.5",
			Description:         "MiniMax M2.5 via WorkBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "kimi-k2.5",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "Kimi K2.5",
			Description:         "Kimi K2.5 via WorkBuddy",
			ContextLength:       256000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "kimi-k2.6",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "Kimi K2.6",
			Description:         "Kimi K2.6 via WorkBuddy",
			ContextLength:       256000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "kimi-k2-thinking",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "Kimi K2 Thinking",
			Description:         "Kimi K2 Thinking via WorkBuddy",
			ContextLength:       256000,
			MaxCompletionTokens: 32768,
			Thinking:            &ThinkingSupport{ZeroAllowed: true},
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "deepseek-v3-2-volc",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "DeepSeek V3.2 (Volc)",
			Description:         "DeepSeek V3.2 via WorkBuddy",
			ContextLength:       128000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "hy3-preview",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "workbuddy",
			DisplayName:         "Hy3 Preview",
			Description:         "Hy3 Preview via WorkBuddy",
			ContextLength:       128000,
			MaxCompletionTokens: 32768,
			Thinking:            &ThinkingSupport{ZeroAllowed: true},
			SupportedEndpoints:  []string{"/chat/completions"},
		},
	}
}

// GetWorkBuddyGlobalModels returns the static international (www.workbuddy.ai) catalog.
// Live accounts prefer GET /v3/config; this list is the fallback captured from the CLI agent.
func GetWorkBuddyGlobalModels() []*ModelInfo {
	now := int64(1748044800) // 2025-05-24
	ids := []string{
		"default-model",
		"fast-model",
		"balanced-model",
		"primary-model",
		"deep-model",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.3-codex",
		"gemini-3.5-flash",
		"glm-5.3",
		"glm-5.2",
		"hy4-preview",
		"hy3",
		"gpt-6-astra",
		"kimi-k3",
		"kimi-k2.6",
	}
	models := make([]*ModelInfo, 0, len(ids))
	for _, id := range ids {
		models = append(models, NewWorkBuddyModelInfo(id, now))
	}
	return models
}

// NewWorkBuddyModelInfo builds a WorkBuddy-served model and overlays thinking
// and context-window metadata from the WorkBuddy bundled catalog when the ID
// is known. Codex/OpenAI static definitions are not used. Unknown IDs are
// marked user-defined so ApplyThinking forwards caller effort instead of
// stripping it as "unsupported".
func NewWorkBuddyModelInfo(id string, created int64) *ModelInfo {
	id = strings.TrimSpace(id)
	model := &ModelInfo{
		ID:                  id,
		Object:              "model",
		Created:             created,
		OwnedBy:             "tencent",
		Type:                "workbuddy",
		DisplayName:         id,
		Description:         id + " via WorkBuddy",
		ContextLength:       128000,
		MaxCompletionTokens: 32768,
		SupportedEndpoints:  []string{"/chat/completions", "/responses"},
	}
	applyWorkBuddyCatalogCapabilities(model)
	return model
}

func applyWorkBuddyCatalogCapabilities(model *ModelInfo) {
	if model == nil || model.ID == "" {
		return
	}
	if caps, ok := workBuddyGlobalCatalog[model.ID]; ok {
		caps.apply(model)
		model.UserDefined = false
		return
	}
	src := workBuddyCNCapabilitySource(model.ID)
	if src == nil {
		model.UserDefined = true
		return
	}
	if src.ContextLength > 0 {
		model.ContextLength = src.ContextLength
	}
	if src.MaxCompletionTokens > 0 {
		model.MaxCompletionTokens = src.MaxCompletionTokens
	}
	if src.Thinking != nil {
		thinking := *src.Thinking
		if len(src.Thinking.Levels) > 0 {
			thinking.Levels = append([]string(nil), src.Thinking.Levels...)
		}
		model.Thinking = &thinking
	}
	if src.DisplayName != "" {
		model.DisplayName = src.DisplayName
	}
	if src.Description != "" {
		model.Description = src.Description
	}
	if src.Version != "" {
		model.Version = src.Version
	}
	if len(src.SupportedInputModalities) > 0 {
		model.SupportedInputModalities = append([]string(nil), src.SupportedInputModalities...)
	}
	if len(src.SupportedOutputModalities) > 0 {
		model.SupportedOutputModalities = append([]string(nil), src.SupportedOutputModalities...)
	}
	if len(src.SupportedParameters) > 0 {
		model.SupportedParameters = append([]string(nil), src.SupportedParameters...)
	}
}

func workBuddyCNCapabilitySource(id string) *ModelInfo {
	for _, model := range GetWorkBuddyModels() {
		if model != nil && model.ID == id {
			return model
		}
	}
	return nil
}
