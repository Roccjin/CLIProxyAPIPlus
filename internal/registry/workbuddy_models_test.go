package registry

import (
	"strings"
	"testing"
)

func TestGetWorkBuddyModelsIncludesVerifiedBuiltIns(t *testing.T) {
	models := GetWorkBuddyModels()
	byID := make(map[string]*ModelInfo, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		if _, exists := byID[model.ID]; exists {
			t.Fatalf("duplicate WorkBuddy model ID %q", model.ID)
		}
		byID[model.ID] = model
	}

	for _, id := range []string{"hy3-preview", "minimax-m2.5", "kimi-k2.6", "kimi-k2-thinking"} {
		if byID[id] == nil {
			t.Fatalf("expected WorkBuddy model %q to be registered", id)
		}
	}

	if byID["hy3-preview"].Thinking == nil {
		t.Fatal("expected hy3-preview to advertise thinking support")
	}
}

func TestGetWorkBuddyGlobalModelsIncludesInternationalCatalog(t *testing.T) {
	models := GetWorkBuddyGlobalModels()
	byID := make(map[string]*ModelInfo, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		if _, exists := byID[model.ID]; exists {
			t.Fatalf("duplicate WorkBuddy global model ID %q", model.ID)
		}
		byID[model.ID] = model
	}
	for _, id := range []string{"gpt-5.6-luna", "gpt-5.5", "kimi-k2.6", "hy3"} {
		if byID[id] == nil {
			t.Fatalf("expected WorkBuddy global model %q to be registered", id)
		}
	}

	luna := byID["gpt-5.6-luna"]
	if luna.Thinking == nil || len(luna.Thinking.Levels) == 0 {
		t.Fatal("expected gpt-5.6-luna to advertise thinking levels")
	}
	if luna.Thinking.DefaultLevel != "high" {
		t.Fatalf("gpt-5.6-luna default effort = %q, want high", luna.Thinking.DefaultLevel)
	}
	if luna.ContextLength != 1000000 {
		t.Fatalf("gpt-5.6-luna context_length = %d, want 1000000", luna.ContextLength)
	}
	if luna.MaxCompletionTokens != 128000 {
		t.Fatalf("gpt-5.6-luna max_completion_tokens = %d, want 128000", luna.MaxCompletionTokens)
	}
	if luna.Type != "workbuddy" {
		t.Fatalf("type = %s, want workbuddy", luna.Type)
	}

	hy3 := byID["hy3"]
	if hy3 == nil {
		t.Fatal("expected hy3")
	}
	if hy3.ContextLength != 192000 {
		t.Fatalf("hy3 context_length = %d, want 192000", hy3.ContextLength)
	}
	if hy3.MaxCompletionTokens != 64000 {
		t.Fatalf("hy3 max_completion_tokens = %d, want 64000", hy3.MaxCompletionTokens)
	}
	if hy3.Thinking == nil || hy3.Thinking.DefaultLevel != "high" {
		t.Fatalf("hy3 thinking = %#v, want default high", hy3.Thinking)
	}
	if got := strings.Join(hy3.Thinking.Levels, ","); got != "low,high" {
		t.Fatalf("hy3 levels = %q, want low,high", got)
	}
}

func TestNewWorkBuddyModelInfoUnknownIDIsUserDefined(t *testing.T) {
	model := NewWorkBuddyModelInfo("not-a-workbuddy-model", 1)
	if model == nil {
		t.Fatal("expected model")
	}
	if !model.UserDefined {
		t.Fatal("unknown WorkBuddy IDs must be user-defined so thinking is not stripped")
	}
	if model.Thinking != nil {
		t.Fatal("unknown IDs should not invent thinking metadata")
	}
}

func TestNewWorkBuddyModelInfoDoesNotUseCodexWindows(t *testing.T) {
	model := NewWorkBuddyModelInfo("gpt-5.6-luna", 1)
	if model.UserDefined {
		t.Fatal("gpt-5.6-luna should come from the WorkBuddy catalog")
	}
	if static := LookupStaticModelInfo("gpt-5.6-luna"); static != nil && static.ContextLength == model.ContextLength && static.ContextLength != 1000000 {
		t.Fatalf("WorkBuddy luna window collapsed to Codex catalog %d", static.ContextLength)
	}
}

func TestNewWorkBuddyModelInfoUsesCNThinkingFallback(t *testing.T) {
	model := NewWorkBuddyModelInfo("hy3-preview", 1)
	if model.Thinking == nil {
		t.Fatal("expected hy3-preview thinking from the CN catalog")
	}
}
