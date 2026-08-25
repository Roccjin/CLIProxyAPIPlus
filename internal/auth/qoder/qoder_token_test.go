package qoder

import (
	"bytes"
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
