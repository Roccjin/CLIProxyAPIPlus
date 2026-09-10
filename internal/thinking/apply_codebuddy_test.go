package thinking_test

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/thinking/provider/openai"
	"github.com/tidwall/gjson"
)

func TestApplyThinkingCodeBuddyLunaKeepsReasoningEffort(t *testing.T) {
	clientID := t.Name()
	registry.GetGlobalRegistry().RegisterClient(clientID, "codebuddy", registry.GetCodeBuddyGlobalModels())
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(clientID)
	})

	body := []byte(`{"model":"gpt-5.6-luna","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`)
	out, err := thinking.ApplyThinking(body, "gpt-5.6-luna", "openai-response", "openai", "codebuddy")
	if err != nil {
		t.Fatalf("ApplyThinking() error = %v", err)
	}
	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "high" {
		t.Fatalf("reasoning_effort = %q, want high; body=%s", got, out)
	}
}
