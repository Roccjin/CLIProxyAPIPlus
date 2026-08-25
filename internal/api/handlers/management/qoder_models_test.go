package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	qoderauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qoder"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestGetQoderModels_FallbackWhenNoAuth(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/qoder-models", nil)
	h.GetQoderModels(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Models []struct {
			Key          string   `json:"key"`
			ContextSizes []string `json:"context_sizes"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, model := range payload.Models {
		if model.Key == "dfmodel" {
			found = true
			if len(model.ContextSizes) == 0 {
				t.Fatal("dfmodel missing context_sizes")
			}
		}
	}
	if !found {
		t.Fatalf("expected fallback dfmodel, got %#v", payload.Models)
	}
}

func TestCollectQoderCatalog_FromCachedModelConfig(t *testing.T) {
	storage := &qoderauth.QoderTokenStorage{}
	storage.SetModelConfigs(map[string]json.RawMessage{
		"dfmodel": json.RawMessage(`{
			"key": "dfmodel",
			"display_name": "DeepSeek-V4-Flash",
			"context_config": {
				"200K": {"is_default": true, "token_count": 200000},
				"1M": {"token_count": 1000000}
			}
		}`),
	})
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "qoder-pat",
		Provider: "qoder",
		Storage:  storage,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	h := &Handler{cfg: &config.Config{}, authManager: manager}
	models := h.collectQoderCatalog()
	if len(models) != 1 || models[0].Key != "dfmodel" {
		t.Fatalf("catalog = %#v", models)
	}
	if models[0].CatalogContext != "200K" {
		t.Fatalf("catalog_context = %q", models[0].CatalogContext)
	}
}
