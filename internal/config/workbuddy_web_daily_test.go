package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigOptional_WorkBuddyWebDailyDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.WorkBuddyWebDaily.Enabled {
		t.Fatal("expected workbuddy web daily enabled by default")
	}
	if cfg.WorkBuddyWebDaily.Interval != 24*time.Hour {
		t.Fatalf("interval = %s, want 24h", cfg.WorkBuddyWebDaily.Interval)
	}
	if cfg.WorkBuddyWebDaily.Model != DefaultWorkBuddyWebDailyModel {
		t.Fatalf("model = %q", cfg.WorkBuddyWebDaily.Model)
	}
	if cfg.WorkBuddyWebDaily.RequestTimeout != 180*time.Second {
		t.Fatalf("request-timeout = %s", cfg.WorkBuddyWebDaily.RequestTimeout)
	}
}

func TestLoadConfigOptional_WorkBuddyWebDailyCanDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	payload := []byte("workbuddy-web-daily:\n  enabled: false\n  interval: 12h\n  model: glm-5.2\n")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.WorkBuddyWebDaily.Enabled {
		t.Fatal("expected workbuddy web daily disabled")
	}
	if cfg.WorkBuddyWebDaily.Interval != 12*time.Hour {
		t.Fatalf("interval = %s, want 12h", cfg.WorkBuddyWebDaily.Interval)
	}
	if cfg.WorkBuddyWebDaily.Model != "glm-5.2" {
		t.Fatalf("model = %q", cfg.WorkBuddyWebDaily.Model)
	}
	if cfg.WorkBuddyWebDaily.MinAccountInterval != 45*time.Second {
		t.Fatalf("unspecified min-account-interval lost default: %s", cfg.WorkBuddyWebDaily.MinAccountInterval)
	}
}
