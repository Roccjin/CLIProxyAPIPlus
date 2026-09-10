package registry

import "testing"

func TestGetCodeBuddyModelsIncludesVerifiedBuiltIns(t *testing.T) {
	models := GetCodeBuddyModels()
	byID := make(map[string]*ModelInfo, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		if _, exists := byID[model.ID]; exists {
			t.Fatalf("duplicate CodeBuddy model ID %q", model.ID)
		}
		byID[model.ID] = model
	}

	for _, id := range []string{"hy3-preview", "minimax-m2.5", "kimi-k2.6", "kimi-k2-thinking"} {
		if byID[id] == nil {
			t.Fatalf("expected CodeBuddy model %q to be registered", id)
		}
	}

	if byID["hy3-preview"].Thinking == nil {
		t.Fatal("expected hy3-preview to advertise thinking support")
	}
}

func TestGetCodeBuddyGlobalModelsIncludesInternationalCatalog(t *testing.T) {
	models := GetCodeBuddyGlobalModels()
	byID := make(map[string]*ModelInfo, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		if _, exists := byID[model.ID]; exists {
			t.Fatalf("duplicate CodeBuddy global model ID %q", model.ID)
		}
		byID[model.ID] = model
	}
	for _, id := range []string{"gpt-5.6-luna", "gpt-5.5", "kimi-k2.6", "minimax-m3"} {
		if byID[id] == nil {
			t.Fatalf("expected CodeBuddy global model %q to be registered", id)
		}
	}

	luna := byID["gpt-5.6-luna"]
	if luna.Thinking == nil || len(luna.Thinking.Levels) == 0 {
		t.Fatal("expected gpt-5.6-luna to advertise thinking levels")
	}
	if luna.ContextLength < 272000 {
		t.Fatalf("gpt-5.6-luna context_length = %d, want at least 272000", luna.ContextLength)
	}
	if luna.Type != "codebuddy" {
		t.Fatalf("type = %s, want codebuddy", luna.Type)
	}
}

func TestNewCodeBuddyModelInfoUnknownIDIsUserDefined(t *testing.T) {
	model := NewCodeBuddyModelInfo("default-model", 1)
	if model == nil {
		t.Fatal("expected model")
	}
	if !model.UserDefined {
		t.Fatal("unknown CodeBuddy IDs must be user-defined so thinking is not stripped")
	}
	if model.Thinking != nil {
		t.Fatal("unknown IDs should not invent thinking metadata")
	}
}

func TestNewCodeBuddyModelInfoUsesCNThinkingFallback(t *testing.T) {
	model := NewCodeBuddyModelInfo("hy3-preview", 1)
	if model.Thinking == nil {
		t.Fatal("expected hy3-preview thinking from the CN catalog")
	}
}
