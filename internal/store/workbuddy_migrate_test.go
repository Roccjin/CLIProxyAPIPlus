package store

import "testing"

func TestProviderFromAuthMetadataMigratesLegacyWorkBuddy(t *testing.T) {
	t.Parallel()

	metadata := map[string]any{
		"type":   "codebuddy",
		"domain": "www.workbuddy.ai",
	}
	if got := providerFromAuthMetadata(metadata); got != "workbuddy" {
		t.Fatalf("provider = %q", got)
	}
	if got, _ := metadata["type"].(string); got != "workbuddy" {
		t.Fatalf("type = %q", got)
	}

	if got := providerFromAuthMetadata(map[string]any{"type": "codebuddy", "domain": "www.codebuddy.cn"}); got != "codebuddy" {
		t.Fatalf("codebuddy.cn provider = %q", got)
	}
}
