package misc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeMetadata(t *testing.T) {
	source := map[string]any{
		"type":         "codex",
		"access_token": "token-123",
	}
	metadata := map[string]any{
		"disabled":   false,
		"email":      "test@example.com",
		"prefix":     "custom-prefix",
		"websockets": false,
		"note":       "custom note",
	}

	result, err := MergeMetadata(source, metadata)
	if err != nil {
		t.Fatalf("MergeMetadata() error = %v", err)
	}

	if result["type"] != "codex" {
		t.Errorf("type = %v, want codex", result["type"])
	}
	if result["access_token"] != "token-123" {
		t.Errorf("access_token = %v, want token-123", result["access_token"])
	}
	if result["disabled"] != false {
		t.Errorf("disabled = %v, want false", result["disabled"])
	}
	if result["email"] != "test@example.com" {
		t.Errorf("email = %v, want test@example.com", result["email"])
	}
	if result["prefix"] != "custom-prefix" {
		t.Errorf("prefix = %v, want custom-prefix", result["prefix"])
	}
	if result["websockets"] != false {
		t.Errorf("websockets = %v, want false", result["websockets"])
	}
	if result["note"] != "custom note" {
		t.Errorf("note = %v, want custom note", result["note"])
	}
}

func TestPreserveAuthFileMetadata_KeepsDisabledAndDropsStaleToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	existing := []byte(`{"type":"qoder","access_token":"old-token","disabled":true,"prefix":"team","proxy_url":"http://proxy:8080"}`)
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	data := map[string]any{
		"type":         "qoder",
		"access_token": "new-token",
	}
	PreserveAuthFileMetadata(path, data)

	if data["access_token"] != "new-token" {
		t.Fatalf("access_token = %v, want new-token", data["access_token"])
	}
	if disabled, _ := data["disabled"].(bool); !disabled {
		t.Fatalf("disabled = %v, want true", data["disabled"])
	}
	if data["prefix"] != "team" {
		t.Fatalf("prefix = %v, want team", data["prefix"])
	}
	if data["proxy_url"] != "http://proxy:8080" {
		t.Fatalf("proxy_url = %v, want http://proxy:8080", data["proxy_url"])
	}
}

func TestPreserveAuthFileMetadata_DoesNotOverrideExplicitDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"type":"claude","disabled":true}`), 0o600); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	data := map[string]any{
		"type":     "claude",
		"disabled": false,
	}
	PreserveAuthFileMetadata(path, data)
	if disabled, _ := data["disabled"].(bool); disabled {
		t.Fatal("explicit disabled=false was overwritten by existing file")
	}
}

func TestPreserveAuthFileMetadata_DoesNotCopyMissingAccessToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"type":"claude","access_token":"stale","disabled":true}`), 0o600); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	data := map[string]any{"type": "claude"}
	PreserveAuthFileMetadata(path, data)
	if _, exists := data["access_token"]; exists {
		t.Fatalf("stale access_token was copied: %#v", data["access_token"])
	}
	if disabled, _ := data["disabled"].(bool); !disabled {
		t.Fatal("disabled flag was not preserved")
	}
}
