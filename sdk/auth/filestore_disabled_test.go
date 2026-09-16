package auth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type testTokenStorage struct {
	meta map[string]any
}

func (s *testTokenStorage) SetMetadata(meta map[string]any) { s.meta = meta }

func (s *testTokenStorage) SaveTokenToFile(authFilePath string) error {
	raw, err := json.Marshal(s.meta)
	if err != nil {
		return err
	}
	return os.WriteFile(authFilePath, raw, 0o600)
}

func TestFileTokenStore_Save_DisabledPersistsFlagForTokenStorage(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "disabled.json")

	if err := os.WriteFile(path, []byte(`{"type":"test","disabled":true}`), 0o600); err != nil {
		t.Fatalf("seed auth file: %v", err)
	}

	store := NewFileTokenStore()
	store.SetBaseDir(baseDir)
	storage := &testTokenStorage{}

	auth := &cliproxyauth.Auth{
		ID:       "disabled.json",
		Provider: "test",
		FileName: "disabled.json",
		Disabled: true,
		Storage:  storage,
		Metadata: map[string]any{"type": "test"},
	}

	if _, err := store.Save(ctx, auth); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth file: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal auth file: %v", err)
	}
	if disabled, _ := meta["disabled"].(bool); !disabled {
		t.Fatalf("disabled=%v, want true (raw=%s)", meta["disabled"], string(raw))
	}
}

func TestFileTokenStore_Save_WorkBuddyStoragePersistsDisabledMetadata(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "wb-disabled.json")

	if err := os.WriteFile(path, []byte(`{"type":"workbuddy","access_token":"old","disabled":false}`), 0o600); err != nil {
		t.Fatalf("seed auth file: %v", err)
	}

	store := NewFileTokenStore()
	store.SetBaseDir(baseDir)
	storage := &workbuddy.WorkBuddyTokenStorage{
		AccessToken:  "new-access",
		RefreshToken: "rt",
	}

	auth := &cliproxyauth.Auth{
		ID:       "wb-disabled.json",
		Provider: "workbuddy",
		FileName: "wb-disabled.json",
		Disabled: true,
		Storage:  storage,
		Metadata: map[string]any{
			"type":                   "workbuddy",
			"disabled_reason":        "credits_exhausted",
			"disabled_provider_code": "14018",
			"disabled_at":            "2026-09-16T00:00:00Z",
		},
	}

	if _, err := store.Save(ctx, auth); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth file: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal auth file: %v", err)
	}
	if meta["access_token"] != "new-access" {
		t.Fatalf("access_token=%v, want new-access", meta["access_token"])
	}
	if disabled, _ := meta["disabled"].(bool); !disabled {
		t.Fatalf("disabled=%v, want true (raw=%s)", meta["disabled"], string(raw))
	}
	if meta["disabled_reason"] != "credits_exhausted" {
		t.Fatalf("disabled_reason=%v", meta["disabled_reason"])
	}
	if meta["disabled_provider_code"] != "14018" {
		t.Fatalf("disabled_provider_code=%v", meta["disabled_provider_code"])
	}
	if meta["disabled_at"] != "2026-09-16T00:00:00Z" {
		t.Fatalf("disabled_at=%v", meta["disabled_at"])
	}
	if _, exists := meta["Metadata"]; exists {
		t.Fatalf("nested Metadata key leaked into JSON: %s", raw)
	}
}
