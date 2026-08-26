package qoder

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveTokenToFile_SkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qoder-user-pat-test.json")
	storage := &QoderTokenStorage{
		AuthMode:      AuthModePAT,
		PersonalToken: "pt-testtoken",
		Token:         "jt-session",
		RefreshToken:  "jrt-1",
		Email:         "user@example.com",
		ExpireTime:    time.Now().Add(24 * time.Hour).UnixMilli(),
		Type:          "qoder",
	}
	if err := storage.SaveTokenToFile(path); err != nil {
		t.Fatalf("first SaveTokenToFile: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	if err := storage.SaveTokenToFile(path); err != nil {
		t.Fatalf("second SaveTokenToFile: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("SaveTokenToFile replaced an unchanged auth file")
	}

	storage.Token = "jt-rotated"
	if err := storage.SaveTokenToFile(path); err != nil {
		t.Fatalf("third SaveTokenToFile: %v", err)
	}
	changed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after change: %v", err)
	}
	if !bytes.Contains(changed, []byte("jt-rotated")) {
		t.Fatalf("expected rotated token in file, got %s", changed)
	}
}

func TestSaveTokenToFile_IgnoresLastRefresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qoder-user-pat-test.json")
	storage := &QoderTokenStorage{
		AuthMode:      AuthModePAT,
		PersonalToken: "pt-testtoken",
		Token:         "jt-session",
		Email:         "user@example.com",
		LastRefresh:   "2026-08-26T10:00:00Z",
		Type:          "qoder",
	}
	if err := storage.SaveTokenToFile(path); err != nil {
		t.Fatalf("first save: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	storage.LastRefresh = "2026-08-26T11:00:00Z"
	if err := storage.SaveTokenToFile(path); err != nil {
		t.Fatalf("second save: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("last_refresh-only change rewrote the auth file")
	}
}

func TestSaveTokenToFile_StaleMetadataDoesNotOverwriteToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qoder-user-pat-test.json")
	storage := &QoderTokenStorage{
		AuthMode:      AuthModePAT,
		PersonalToken: "pt-testtoken",
		Token:         "jt-session",
		Email:         "user@example.com",
		Type:          "qoder",
	}
	storage.SetMetadata(map[string]any{
		"token":         "jt-stale-should-not-win",
		"model_configs": map[string]any{},
		"disabled":      false,
	})
	storage.SetModelConfigs(map[string]json.RawMessage{
		"dfmodel": json.RawMessage(`{"key":"dfmodel"}`),
	})
	if err := storage.SaveTokenToFile(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Contains(raw, []byte("jt-session")) {
		t.Fatalf("expected live token, got %s", raw)
	}
	if bytes.Contains(raw, []byte("jt-stale-should-not-win")) {
		t.Fatal("stale metadata token overwrote live storage")
	}
	if !bytes.Contains(raw, []byte("dfmodel")) {
		t.Fatalf("expected model_configs to persist, got %s", raw)
	}
}
