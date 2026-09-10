package thinking_test

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/thinking/provider/openai"
	"github.com/tidwall/gjson"
)

func TestApplyThinkingWorkBuddyHy3ClampsUnsupportedEffort(t *testing.T) {
	model := registry.NewWorkBuddyModelInfo("hy3", 1)
	body := []byte(`{"model":"hy3","reasoning_effort":"xhigh","messages":[{"role":"user","content":"hi"}]}`)
	out, err := thinking.ApplyThinkingWithModelInfo(body, body, "hy3", "openai", "openai", "workbuddy", model)
	if err != nil {
		t.Fatalf("ApplyThinkingWithModelInfo() error = %v", err)
	}
	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "high" {
		t.Fatalf("hy3 xhigh clamped to %q, want high; body=%s", got, out)
	}
}

func TestApplyThinkingWorkBuddyLunaKeepsXHigh(t *testing.T) {
	model := registry.NewWorkBuddyModelInfo("gpt-5.6-luna", 1)
	body := []byte(`{"model":"gpt-5.6-luna","reasoning_effort":"xhigh","messages":[{"role":"user","content":"hi"}]}`)
	out, err := thinking.ApplyThinkingWithModelInfo(body, body, "gpt-5.6-luna", "openai", "openai", "workbuddy", model)
	if err != nil {
		t.Fatalf("ApplyThinkingWithModelInfo() error = %v", err)
	}
	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "xhigh" {
		t.Fatalf("reasoning_effort = %q, want xhigh; body=%s", got, out)
	}
}
