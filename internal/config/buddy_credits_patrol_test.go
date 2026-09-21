package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigOptional_BuddyCreditsPatrolDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.BuddyCreditsPatrol.Enabled {
		t.Fatal("expected buddy credits patrol enabled by default")
	}
	if cfg.BuddyCreditsPatrol.Interval != 12*time.Hour {
		t.Fatalf("interval = %s, want 12h", cfg.BuddyCreditsPatrol.Interval)
	}
	if cfg.BuddyCreditsPatrol.MinAccountInterval != 45*time.Second {
		t.Fatalf("min-account-interval = %s", cfg.BuddyCreditsPatrol.MinAccountInterval)
	}
	if cfg.BuddyCreditsPatrol.MinRemain != 1 {
		t.Fatalf("min-remain = %v", cfg.BuddyCreditsPatrol.MinRemain)
	}
}

func TestLoadConfigOptional_BuddyCreditsPatrolCanDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	payload := []byte("buddy-credits-patrol:\n  enabled: false\n  interval: 6h\n")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if cfg.BuddyCreditsPatrol.Enabled {
		t.Fatal("expected buddy credits patrol disabled")
	}
	if cfg.BuddyCreditsPatrol.Interval != 6*time.Hour {
		t.Fatalf("interval = %s, want 6h", cfg.BuddyCreditsPatrol.Interval)
	}
	if cfg.BuddyCreditsPatrol.MinAccountInterval != 45*time.Second {
		t.Fatalf("unspecified min-account-interval lost default: %s", cfg.BuddyCreditsPatrol.MinAccountInterval)
	}
}

func TestBuddyCreditsPatrolConfigNormalize(t *testing.T) {
	t.Parallel()

	cfg := BuddyCreditsPatrolConfig{Enabled: false, Interval: 0, MinAccountInterval: -time.Second, AccountJitter: -1, MinRemain: -3, RequestTimeout: 0}
	cfg.Normalize()
	if cfg.Enabled {
		t.Fatal("Normalize must not turn patrol on")
	}
	if cfg.Interval != 12*time.Hour || cfg.MinAccountInterval != 45*time.Second || cfg.RequestTimeout != 45*time.Second {
		t.Fatalf("normalized durations = %+v", cfg)
	}
	if cfg.AccountJitter != 0 || cfg.MinRemain != 0 {
		t.Fatalf("normalized jitter/remain = %+v", cfg)
	}
}

func TestBuddyCreditsPatrolConfig_YAMLRoundTripDurationStrings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\nbuddy-credits-patrol:\n  enabled: true\n  interval: 12h\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfigOptional(path, false)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.BuddyCreditsPatrol.Interval = 6 * time.Hour
	if err := SaveConfigPreserveComments(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "interval: 6h") {
		t.Fatalf("saved yaml missing 6h interval:\n%s", data)
	}
	if strings.Contains(string(data), "21600000000000") {
		t.Fatalf("saved yaml used nanoseconds:\n%s", data)
	}
}
