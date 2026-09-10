package misc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

// Separator used to visually group related log lines.
var credentialSeparator = strings.Repeat("-", 67)

// LogSavingCredentials emits a consistent log message when persisting auth material.
func LogSavingCredentials(path string) {
	if path == "" {
		return
	}
	// Use filepath.Clean so logs remain stable even if callers pass redundant separators.
	fmt.Printf("Saving credentials to %s\n", filepath.Clean(path))
}

// LogCredentialSeparator adds a visual separator to group auth/key processing logs.
func LogCredentialSeparator() {
	log.Debug(credentialSeparator)
}

// MergeMetadata serializes the source struct into a map and merges the provided metadata into it.
func MergeMetadata(source any, metadata map[string]any) (map[string]any, error) {
	var data map[string]any

	// Fast path: if source is already a map, just copy it to avoid mutation of original
	if srcMap, ok := source.(map[string]any); ok {
		data = make(map[string]any, len(srcMap)+len(metadata))
		for k, v := range srcMap {
			data[k] = v
		}
	} else if source != nil {
		// Slow path: marshal to JSON and back to map to respect JSON tags
		temp, errMarshal := json.Marshal(source)
		if errMarshal != nil {
			return nil, fmt.Errorf("failed to marshal source: %w", errMarshal)
		}
		if errUnmarshal := json.Unmarshal(temp, &data); errUnmarshal != nil {
			return nil, fmt.Errorf("failed to unmarshal to map: %w", errUnmarshal)
		}
	}

	// Merge extra metadata
	if metadata != nil {
		if data == nil {
			data = make(map[string]any)
		}
		for k, v := range metadata {
			data[k] = v
		}
	}

	return data, nil
}

// MergeAndPreserveAuthFile serializes source, overlays metadata, then copies
// non-credential keys from the existing auth JSON when the outgoing map does
// not already define them. Use this from SaveTokenToFile so fields such as
// "disabled", "prefix", and "proxy_url" survive refresh writes that skip
// FileTokenStore.Save.
func MergeAndPreserveAuthFile(source any, metadata map[string]any, authFilePath string) (map[string]any, error) {
	data, err := MergeMetadata(source, metadata)
	if err != nil {
		return nil, err
	}
	if data == nil {
		data = make(map[string]any)
	}
	PreserveAuthFileMetadata(authFilePath, data)
	return data, nil
}

// PreserveAuthFileMetadata copies keys from the existing auth JSON into data
// when data does not already contain them. Credential and token-lifecycle keys
// are never copied from disk so a refresh cannot resurrect stale secrets.
func PreserveAuthFileMetadata(authFilePath string, data map[string]any) {
	if data == nil {
		return
	}
	authFilePath = strings.TrimSpace(authFilePath)
	if authFilePath == "" {
		return
	}
	raw, err := os.ReadFile(authFilePath)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	var existing map[string]any
	if err = json.Unmarshal(raw, &existing); err != nil || len(existing) == 0 {
		return
	}
	for key, value := range existing {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, exists := data[key]; exists {
			continue
		}
		if isAuthCredentialKey(key) {
			continue
		}
		data[key] = value
	}
}

func isAuthCredentialKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "access_token", "refresh_token", "id_token", "session_id",
		"token", "personal_token", "openapi_job_token", "machine_token",
		"api_key", "cookie", "client_secret", "kilocodetoken",
		"expired", "last_refresh", "expires_in", "expire_time",
		"expires_at", "expiresat", "timestamp", "token_type", "user_code",
		"verification_uri", "verification_uri_complete":
		return true
	default:
		return false
	}
}
