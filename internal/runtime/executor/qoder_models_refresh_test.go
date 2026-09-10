package executor

import (
	"context"
	"strings"
	"testing"

	qoderauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qoder"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestParseQoderModelListBody(t *testing.T) {
	t.Parallel()

	models, configs, err := parseQoderModelListBody([]byte(`{
		"chat": [
			{"key":"dfmodel","enable":true,"display_name":"DeepSeek","max_input_tokens":200000,"is_vl":true},
			{"key":"disabled","enable":false,"display_name":"Hidden"},
			{"key":"","enable":true}
		]
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 1 || models[0].ID != "qoder/dfmodel" {
		t.Fatalf("models = %#v", models)
	}
	if models[0].DisplayName != "DeepSeek" || models[0].ContextLength != 200000 {
		t.Fatalf("model = %#v", models[0])
	}
	if len(models[0].SupportedInputModalities) != 2 {
		t.Fatalf("vl modalities = %#v", models[0].SupportedInputModalities)
	}
	if _, ok := configs["dfmodel"]; !ok {
		t.Fatalf("configs = %v", configs)
	}
	if _, ok := configs["disabled"]; ok {
		t.Fatal("disabled model should not be cached")
	}
}

func TestParseQoderModelListBody_MissingChat(t *testing.T) {
	t.Parallel()
	if _, _, err := parseQoderModelListBody([]byte(`{}`)); err == nil {
		t.Fatal("expected missing chat error")
	}
}

func TestParseQoderModelListBody_NoEnabledModels(t *testing.T) {
	t.Parallel()
	if _, _, err := parseQoderModelListBody([]byte(`{"chat":[{"key":"x","enable":false}]}`)); err == nil {
		t.Fatal("expected no enabled models error")
	}
}

func TestRefreshQoderModelsMissingToken(t *testing.T) {
	t.Parallel()
	_, err := RefreshQoderModels(context.Background(), &cliproxyauth.Auth{
		Provider: "qoder",
		Storage:  &qoderauth.QoderTokenStorage{},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing session token") {
		t.Fatalf("err = %v", err)
	}
}

func TestRefreshQoderModelsNilAuth(t *testing.T) {
	t.Parallel()
	_, err := RefreshQoderModels(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected nil auth error")
	}
}

func TestParseQoderModelListBody_ThinkingConfig(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"chat":[{"key":"dmodel","enable":true,"is_reasoning":true,"thinking_config":{"disabled":{},"enabled":{"efforts":{"high":{},"max":{}}}}}]}`)
	models, _, err := parseQoderModelListBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	if models[0].Thinking == nil || !models[0].Thinking.ZeroAllowed {
		t.Fatalf("thinking = %#v", models[0].Thinking)
	}
	if len(models[0].Thinking.Levels) != 2 {
		t.Fatalf("levels = %#v", models[0].Thinking.Levels)
	}
}
