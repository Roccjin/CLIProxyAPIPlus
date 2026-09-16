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

func TestSaveTokenToFile_MergesInjectedMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workbuddy-user.json")
	seed := []byte(`{"type":"workbuddy","access_token":"old","refresh_token":"rt","disabled":false}`)
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatalf("seed auth file: %v", err)
	}

	storage := &WorkBuddyTokenStorage{
		AccessToken:  "new-access",
		RefreshToken: "rt",
	}
	storage.SetMetadata(map[string]any{
		"type":                   "workbuddy",
		"disabled":               true,
		"disabled_reason":        "credits_exhausted",
		"disabled_provider_code": "14018",
		"disabled_at":            "2026-09-16T00:00:00Z",
	})
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
	if saved["disabled_reason"] != "credits_exhausted" {
		t.Fatalf("disabled_reason = %v", saved["disabled_reason"])
	}
	if saved["disabled_provider_code"] != "14018" {
		t.Fatalf("disabled_provider_code = %v", saved["disabled_provider_code"])
	}
	if saved["disabled_at"] != "2026-09-16T00:00:00Z" {
		t.Fatalf("disabled_at = %v", saved["disabled_at"])
	}
	if _, exists := saved["Metadata"]; exists {
		t.Fatalf("nested Metadata key leaked into JSON: %s", raw)
	}
	if _, exists := saved["metadata"]; exists {
		t.Fatalf("nested metadata key leaked into JSON: %s", raw)
	}
}
