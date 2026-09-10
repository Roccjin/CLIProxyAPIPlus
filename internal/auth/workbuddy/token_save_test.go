package workbuddy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveTokenToFile_PreservesDisabledWithoutMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codebuddy-user.json")
	seed := []byte(`{"type":"workbuddy","access_token":"old","refresh_token":"rt","disabled":true,"prefix":"team"}`)
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatalf("seed auth file: %v", err)
	}

	storage := &WorkBuddyTokenStorage{
		AccessToken:  "new-access",
		RefreshToken: "rt",
		Type:         "workbuddy",
	}
	if err := storage.SaveTokenToFile(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if saved["access_token"] != "new-access" {
		t.Fatalf("access_token = %v, want new-access", saved["access_token"])
	}
	if disabled, _ := saved["disabled"].(bool); !disabled {
		t.Fatalf("disabled = %v, want true; body=%s", saved["disabled"], raw)
	}
	if saved["prefix"] != "team" {
		t.Fatalf("prefix = %v, want team", saved["prefix"])
	}
}
