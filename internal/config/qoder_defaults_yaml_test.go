package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveConfigPreserveComments_WritesQoderModelDefaults(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte(`debug: true

# qoder-model-defaults:
#   ultimate:
#     context: 400K
`)
	if errWrite := os.WriteFile(configPath, original, 0o600); errWrite != nil {
		t.Fatalf("WriteFile: %v", errWrite)
	}
	cfg := &Config{
		Debug: true,
		QoderModelDefaults: map[string]QoderModelDefault{
			"dfmodel": {Context: "1M"},
		},
	}
	if errSave := SaveConfigPreserveComments(configPath, cfg); errSave != nil {
		t.Fatalf("SaveConfigPreserveComments: %v", errSave)
	}
	saved, errRead := os.ReadFile(configPath)
	if errRead != nil {
		t.Fatalf("ReadFile: %v", errRead)
	}
	text := string(saved)
	if !strings.Contains(text, "qoder-model-defaults:") || !strings.Contains(text, "dfmodel:") || !strings.Contains(text, "1M") {
		t.Fatalf("saved config missing qoder defaults:\n%s", text)
	}

	loaded, errParse := ParseConfigBytes(saved)
	if errParse != nil {
		t.Fatalf("ParseConfigBytes: %v", errParse)
	}
	if loaded.QoderModelDefaults["dfmodel"].Context != "1M" {
		t.Fatalf("reloaded defaults = %#v", loaded.QoderModelDefaults)
	}
}
