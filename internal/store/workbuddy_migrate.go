package store

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
)

func providerFromAuthMetadata(metadata map[string]any) string {
	workbuddy.MigrateLegacyCodeBuddyMetadata(metadata)
	provider := strings.TrimSpace(valueAsString(metadata["type"]))
	if provider == "" {
		return "unknown"
	}
	return provider
}
