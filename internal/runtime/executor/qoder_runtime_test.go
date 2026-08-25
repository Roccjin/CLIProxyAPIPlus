package executor

import (
	"encoding/json"
	"strings"
	"testing"

	qoderauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qoder"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestParseQoderModelRequest(t *testing.T) {
	tests := []struct {
		raw, key, effort, ctx string
	}{
		{"qoder/ultimate", "ultimate", "", ""},
		{"qoder/ultimate(high)", "ultimate", "high", ""},
		{"qoder/ultimate[1m]", "ultimate", "", "1M"},
		{"qoder/ultimate(high)[1m]", "ultimate", "high", "1M"},
		{"qoder/ultimate[1m](high)", "ultimate", "high", "1M"},
		{"auto", "auto", "", ""},
	}
	for _, tt := range tests {
		key, effort, ctx := parseQoderModelRequest(tt.raw)
		if key != tt.key || effort != tt.effort || ctx != tt.ctx {
			t.Errorf("parseQoderModelRequest(%q) = %q, %q, %q; want %q, %q, %q",
				tt.raw, key, effort, ctx, tt.key, tt.effort, tt.ctx)
		}
	}
}

func TestApplyQoderRuntimeKnobs_ThinkingAndContext(t *testing.T) {
	on := true
	reqBody := map[string]interface{}{
		"parameters": map[string]interface{}{"max_tokens": 1024},
		"chat_context": map[string]interface{}{
			"extra": map[string]interface{}{
				"modelConfig": map[string]interface{}{"key": "ultimate"},
			},
		},
	}
	modelConfig := map[string]interface{}{"key": "ultimate", "is_reasoning": false}
	applyQoderRuntimeKnobs(reqBody, modelConfig, &on, "high", 400000)

	params := reqBody["parameters"].(map[string]interface{})
	if params["enable_thinking"] != true {
		t.Fatalf("enable_thinking = %#v", params["enable_thinking"])
	}
	if params["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v", params["reasoning_effort"])
	}
	if params["context_length"] != float64(400000) {
		t.Fatalf("context_length = %#v", params["context_length"])
	}
	if modelConfig["is_reasoning"] != true {
		t.Fatalf("model_config.is_reasoning = %#v", modelConfig["is_reasoning"])
	}
	if modelConfig["max_input_tokens"] != float64(400000) {
		t.Fatalf("max_input_tokens = %#v", modelConfig["max_input_tokens"])
	}
	extra := qoderChatContextModelConfig(reqBody)
	if extra["is_reasoning"] != true {
		t.Fatalf("chat_context is_reasoning = %#v", extra["is_reasoning"])
	}
}

func TestApplyQoderRuntimeKnobs_Off(t *testing.T) {
	off := false
	reqBody := map[string]interface{}{
		"parameters": map[string]interface{}{"reasoning_effort": "high"},
	}
	modelConfig := map[string]interface{}{"is_reasoning": true}
	applyQoderRuntimeKnobs(reqBody, modelConfig, &off, "", 0)
	params := reqBody["parameters"].(map[string]interface{})
	if params["enable_thinking"] != false {
		t.Fatalf("enable_thinking = %#v", params["enable_thinking"])
	}
	if _, ok := params["reasoning_effort"]; ok {
		t.Fatal("reasoning_effort should be removed when thinking is off")
	}
	if modelConfig["is_reasoning"] != false {
		t.Fatalf("is_reasoning = %#v", modelConfig["is_reasoning"])
	}
}

func TestLookupQoderModelDefault(t *testing.T) {
	cfg := &config.Config{
		QoderModelDefaults: map[string]config.QoderModelDefault{
			"ultimate": {Thinking: "high", Context: "400K"},
		},
	}
	th, ctx := lookupQoderModelDefault(cfg, "qoder/ultimate")
	if th != "high" || ctx != "400K" {
		t.Fatalf("got %q %q", th, ctx)
	}
}

func TestResolveQoderThinkingPriority(t *testing.T) {
	chatReq := map[string]interface{}{"reasoning_effort": "low", "thinking": "max"}
	enable, effort := resolveQoderThinking(chatReq, "high", "medium")
	if enable == nil || !*enable || effort != "high" {
		t.Fatalf("suffix should win: enable=%v effort=%q", enable, effort)
	}
	enable, effort = resolveQoderThinking(chatReq, "", "medium")
	if enable == nil || !*enable || effort != "low" {
		t.Fatalf("reasoning_effort should win over thinking: enable=%v effort=%q", enable, effort)
	}
	enable, effort = resolveQoderThinking(map[string]interface{}{}, "", "off")
	if enable == nil || *enable {
		t.Fatalf("default off should disable thinking")
	}
}

func TestQoderContextTokensJSONRoundTrip(t *testing.T) {
	if qoderContextTokens("400K") != 400000 {
		t.Fatal("400K")
	}
	raw, _ := json.Marshal(map[string]int{"n": qoderContextTokens("1M")})
	if string(raw) != `{"n":1000000}` {
		t.Fatalf("json = %s", raw)
	}
}

func TestResolveQoderContextTokens_DefaultWhenRequestOmits(t *testing.T) {
	if got := resolveQoderContextTokens(map[string]interface{}{}, "", "1M"); got != 1000000 {
		t.Fatalf("configured default = %d, want 1000000", got)
	}
	if got := resolveQoderContextTokens(map[string]interface{}{"context_size": "400K"}, "", "1M"); got != 400000 {
		t.Fatalf("request context_size should win, got %d", got)
	}
	if got := resolveQoderContextTokens(map[string]interface{}{}, "1M", "400K"); got != 1000000 {
		t.Fatalf("suffix should win, got %d", got)
	}
	if got := resolveQoderContextTokens(map[string]interface{}{}, "", ""); got != 0 {
		t.Fatalf("empty should omit context_length, got %d", got)
	}
}

func TestParseQoderCatalogEntry_ContextConfigDefault(t *testing.T) {
	raw := json.RawMessage(`{
		"key": "dfmodel",
		"display_name": "DeepSeek-V4-Flash",
		"thinking_config": {
			"disabled": {},
			"enabled": {"efforts": {"max": {}, "high": {}}}
		},
		"context_config": {
			"1M": {"token_count": 1000000},
			"200K": {"is_default": true, "token_count": 200000},
			"400K": {"token_count": 400000}
		}
	}`)
	model, ok := ParseQoderCatalogEntry(raw)
	if !ok {
		t.Fatal("expected catalog entry")
	}
	if model.Key != "dfmodel" || model.DisplayName != "DeepSeek-V4-Flash" {
		t.Fatalf("identity = %+v", model)
	}
	if model.CatalogContext != "200K" {
		t.Fatalf("catalog_context = %q, want 200K", model.CatalogContext)
	}
	if stringsJoin(model.ContextSizes) != "200K,400K,1M" {
		t.Fatalf("context_sizes = %v", model.ContextSizes)
	}
	if stringsJoin(model.ThinkingLevels) != "high,max" {
		t.Fatalf("thinking_levels = %v", model.ThinkingLevels)
	}
	if !model.ZeroAllowed {
		t.Fatal("zero_allowed")
	}
}

func TestCollectQoderCatalog_FromCachedModelConfig(t *testing.T) {
	storage := &qoderauth.QoderTokenStorage{}
	storage.SetModelConfigs(map[string]json.RawMessage{
		"dfmodel": json.RawMessage(`{"key":"dfmodel","display_name":"Flash","context_config":{"200K":{"is_default":true,"token_count":200000}}}`),
	})
	models := CollectQoderCatalog(storage)
	if len(models) != 1 || models[0].Key != "dfmodel" || models[0].CatalogContext != "200K" {
		t.Fatalf("catalog = %#v", models)
	}
}

func TestFallbackQoderCatalogIncludesFrontierKeys(t *testing.T) {
	models := FallbackQoderCatalog()
	found := map[string]bool{}
	for _, model := range models {
		found[model.Key] = true
		if len(model.ContextSizes) == 0 {
			t.Fatalf("%s missing context sizes", model.Key)
		}
		if len(model.ThinkingLevels) == 0 {
			t.Fatalf("%s missing thinking levels", model.Key)
		}
	}
	for _, key := range []string{"dfmodel", "dmodel", "ultimate"} {
		if !found[key] {
			t.Fatalf("missing %s", key)
		}
	}
}

func stringsJoin(values []string) string {
	return strings.Join(values, ",")
}
