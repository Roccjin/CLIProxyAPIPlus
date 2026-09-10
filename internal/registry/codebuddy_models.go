package registry

import "strings"

// GetCodeBuddyModels returns the available models for CodeBuddy (Tencent).
// These models are served through the copilot.tencent.com API.
func GetCodeBuddyModels() []*ModelInfo {
	now := int64(1748044800) // 2025-05-24
	return []*ModelInfo{
		{
			ID:                  "auto",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "Auto",
			Description:         "Automatic model selection via CodeBuddy",
			ContextLength:       128000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-5v-turbo",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "GLM-5v Turbo",
			Description:         "GLM-5v Turbo via CodeBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-5.1",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "GLM-5.1",
			Description:         "GLM-5.1 via CodeBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-5.0-turbo",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "GLM-5.0 Turbo",
			Description:         "GLM-5.0 Turbo via CodeBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-5.0",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "GLM-5.0",
			Description:         "GLM-5.0 via CodeBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "glm-4.7",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "GLM-4.7",
			Description:         "GLM-4.7 via CodeBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "minimax-m2.7",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "MiniMax M2.7",
			Description:         "MiniMax M2.7 via CodeBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "minimax-m2.5",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "MiniMax M2.5",
			Description:         "MiniMax M2.5 via CodeBuddy",
			ContextLength:       200000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "kimi-k2.5",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "Kimi K2.5",
			Description:         "Kimi K2.5 via CodeBuddy",
			ContextLength:       256000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "kimi-k2.6",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "Kimi K2.6",
			Description:         "Kimi K2.6 via CodeBuddy",
			ContextLength:       256000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "kimi-k2-thinking",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "Kimi K2 Thinking",
			Description:         "Kimi K2 Thinking via CodeBuddy",
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
			Type:                "codebuddy",
			DisplayName:         "DeepSeek V3.2 (Volc)",
			Description:         "DeepSeek V3.2 via CodeBuddy",
			ContextLength:       128000,
			MaxCompletionTokens: 32768,
			SupportedEndpoints:  []string{"/chat/completions"},
		},
		{
			ID:                  "hy3-preview",
			Object:              "model",
			Created:             now,
			OwnedBy:             "tencent",
			Type:                "codebuddy",
			DisplayName:         "Hy3 Preview",
			Description:         "Hy3 Preview via CodeBuddy",
			ContextLength:       128000,
			MaxCompletionTokens: 32768,
			Thinking:            &ThinkingSupport{ZeroAllowed: true},
			SupportedEndpoints:  []string{"/chat/completions"},
		},
	}
}

// GetCodeBuddyGlobalModels returns the static international (www.codebuddy.ai) catalog.
// Live accounts prefer GET /v3/config; this list is the fallback captured from the CLI agent.
func GetCodeBuddyGlobalModels() []*ModelInfo {
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
		"kimi-k3",
		"kimi-k2.6",
		"minimax-m3",
	}
	models := make([]*ModelInfo, 0, len(ids))
	for _, id := range ids {
		models = append(models, NewCodeBuddyModelInfo(id, now))
	}
	return models
}

// NewCodeBuddyModelInfo builds a CodeBuddy-served model and overlays thinking
// and context-window metadata from the bundled catalog when the ID is known.
// Unknown IDs are marked user-defined so ApplyThinking forwards caller effort
// instead of stripping it as "unsupported".
func NewCodeBuddyModelInfo(id string, created int64) *ModelInfo {
	id = strings.TrimSpace(id)
	model := &ModelInfo{
		ID:                  id,
		Object:              "model",
		Created:             created,
		OwnedBy:             "tencent",
		Type:                "codebuddy",
		DisplayName:         id,
		Description:         id + " via CodeBuddy",
		ContextLength:       128000,
		MaxCompletionTokens: 32768,
		SupportedEndpoints:  []string{"/chat/completions", "/responses"},
	}
	applyCodeBuddyCatalogCapabilities(model)
	return model
}

func applyCodeBuddyCatalogCapabilities(model *ModelInfo) {
	if model == nil || model.ID == "" {
		return
	}
	src := codeBuddyCapabilitySource(model.ID)
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

func codeBuddyCapabilitySource(id string) *ModelInfo {
	if info := LookupStaticModelInfo(id); info != nil {
		return info
	}
	for _, model := range GetCodeBuddyModels() {
		if model != nil && model.ID == id {
			return model
		}
	}
	return nil
}
