package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRefreshCodeBuddyModelsMissingToken(t *testing.T) {
	t.Parallel()
	_, err := RefreshCodeBuddyModels(context.Background(), &cliproxyauth.Auth{}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing access token") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchCodeBuddyModelsFromURL(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != codeBuddyConfigPath {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("authorization = %s", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"agents": []map[string]any{
					{"name": "cli", "models": []string{"gpt-5.6-luna", "kimi-k2.6"}},
				},
			},
		})
	}))
	defer srv.Close()

	auth := &cliproxyauth.Auth{Metadata: map[string]any{
		"access_token": "token",
		"user_id":      "user-1",
		"domain":       codebuddy.DefaultDomainGlobal,
	}}
	models, err := fetchCodeBuddyModelsFromURL(context.Background(), auth, nil, srv.URL+codeBuddyConfigPath, "token", "user-1", codebuddy.DefaultDomainGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "gpt-5.6-luna" || models[1].ID != "kimi-k2.6" {
		t.Fatalf("models = %#v", models)
	}
}

func TestFetchCodeBuddyModelsFromURL_StatusError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := fetchCodeBuddyModelsFromURL(context.Background(), &cliproxyauth.Auth{}, nil, srv.URL+codeBuddyConfigPath, "token", "user-1", codebuddy.DefaultDomainGlobal)
	if err == nil || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("err = %v", err)
	}
}
