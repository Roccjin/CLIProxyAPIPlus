package auth

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestMigrateLegacyWorkBuddyAuth(t *testing.T) {
	t.Parallel()

	auth := &coreauth.Auth{
		ID:       "legacy.json",
		Provider: "codebuddy",
		Metadata: map[string]any{
			"type":          "codebuddy",
			"domain":        "www.workbuddy.ai",
			"access_token":  "tok",
			"refresh_token": "rt",
		},
		Storage: &codebuddy.CodeBuddyTokenStorage{
			AccessToken:  "tok",
			RefreshToken: "rt",
			Domain:       "www.workbuddy.ai",
			Type:         "codebuddy",
		},
	}
	if !MigrateLegacyWorkBuddyAuth(auth) {
		t.Fatal("expected migration")
	}
	if auth.Provider != "workbuddy" {
		t.Fatalf("provider = %q", auth.Provider)
	}
	if got, _ := auth.Metadata["type"].(string); got != "workbuddy" {
		t.Fatalf("metadata type = %q", got)
	}
	storage, ok := auth.Storage.(*workbuddy.WorkBuddyTokenStorage)
	if !ok || storage == nil || storage.Type != "workbuddy" || storage.AccessToken != "tok" {
		t.Fatalf("storage = %#v (%T)", auth.Storage, auth.Storage)
	}

	codebuddyAuth := &coreauth.Auth{
		Provider: "codebuddy",
		Metadata: map[string]any{"type": "codebuddy", "domain": "www.codebuddy.ai"},
	}
	if MigrateLegacyWorkBuddyAuth(codebuddyAuth) {
		t.Fatal("codebuddy.ai must not migrate")
	}
}
