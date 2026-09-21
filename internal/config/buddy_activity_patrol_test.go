package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigOptional_BuddyActivityPatrolDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.BuddyActivityPatrol.Enabled {
		t.Fatal("expected buddy activity patrol enabled by default")
	}
	if cfg.BuddyActivityPatrol.Interval != 24*time.Hour {
		t.Fatalf("interval = %s, want 24h", cfg.BuddyActivityPatrol.Interval)
	}
	if cfg.BuddyActivityPatrol.MinAccountInterval != 45*time.Second {
		t.Fatalf("min-account-interval = %s", cfg.BuddyActivityPatrol.MinAccountInterval)
	}
	if cfg.BuddyActivityPatrol.Model != DefaultBuddyActivityPatrolModel {
		t.Fatalf("model = %q, want %q", cfg.BuddyActivityPatrol.Model, DefaultBuddyActivityPatrolModel)
	}
	if cfg.BuddyActivityPatrol.RequestTimeout != 90*time.Second {
		t.Fatalf("request-timeout = %s", cfg.BuddyActivityPatrol.RequestTimeout)
	}
}

func TestLoadConfigOptional_BuddyActivityPatrolCanDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	payload := []byte("buddy-activity-patrol:\n  enabled: false\n  interval: 12h\n  model: glm-5.2\n")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.BuddyActivityPatrol.Enabled {
		t.Fatal("expected buddy activity patrol disabled")
	}
	if cfg.BuddyActivityPatrol.Interval != 12*time.Hour {
		t.Fatalf("interval = %s, want 12h", cfg.BuddyActivityPatrol.Interval)
	}
	if cfg.BuddyActivityPatrol.Model != "glm-5.2" {
		t.Fatalf("model = %q", cfg.BuddyActivityPatrol.Model)
	}
	if cfg.BuddyActivityPatrol.MinAccountInterval != 45*time.Second {
		t.Fatalf("unspecified min-account-interval lost default: %s", cfg.BuddyActivityPatrol.MinAccountInterval)
	}
}

func TestBuddyActivityPatrolConfigNormalize(t *testing.T) {
	t.Parallel()

	cfg := BuddyActivityPatrolConfig{Enabled: false, Interval: 0, MinAccountInterval: -time.Second, AccountJitter: -1, RequestTimeout: 0, Model: "  "}
	cfg.Normalize()
	if cfg.Enabled {
		t.Fatal("Normalize must not turn patrol on")
	}
	if cfg.Interval != 24*time.Hour || cfg.MinAccountInterval != 45*time.Second || cfg.RequestTimeout != 90*time.Second {
		t.Fatalf("normalized durations = %+v", cfg)
	}
	if cfg.AccountJitter != 0 {
		t.Fatalf("normalized jitter = %+v", cfg)
	}
	if cfg.Model != DefaultBuddyActivityPatrolModel {
		t.Fatalf("normalized model = %q", cfg.Model)
	}
}
