package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestBuildAuthFileEntryExposesDisabledMetadataWhitelist(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	authDir := t.TempDir()
	fileName := "workbuddy-disabled.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"workbuddy"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)
	auth := &coreauth.Auth{
		ID:       "auth-wb-disabled",
		FileName: fileName,
		Provider: "workbuddy",
		Status:   coreauth.StatusDisabled,
		Disabled: true,
		Attributes: map[string]string{
			"path": filePath,
		},
		Metadata: map[string]any{
			"type":                                   "workbuddy",
			"disabled":                               true,
			coreauth.MetadataKeyDisabledReason:       coreauth.DisabledReasonCreditsExhausted,
			coreauth.MetadataKeyDisabledProviderCode: "14018",
			coreauth.MetadataKeyDisabledAt:           "2026-09-16T00:00:00Z",
			"access_token":                           "secret-token-value",
			"refresh_token":                          "secret-refresh-value",
		},
	}

	entry := h.buildAuthFileEntry(auth)
	if entry == nil {
		t.Fatal("entry is nil")
	}
	if got := entry["disabled_reason"]; got != coreauth.DisabledReasonCreditsExhausted {
		t.Fatalf("disabled_reason = %#v", got)
	}
	if got := entry["disabled_provider_code"]; got != "14018" {
		t.Fatalf("disabled_provider_code = %#v", got)
	}
	if got := entry["disabled_at"]; got != "2026-09-16T00:00:00Z" {
		t.Fatalf("disabled_at = %#v", got)
	}
	raw, errMarshal := json.Marshal(entry)
	if errMarshal != nil {
		t.Fatalf("marshal entry: %v", errMarshal)
	}
	if strings.Contains(string(raw), "secret-token-value") || strings.Contains(string(raw), "secret-refresh-value") {
		t.Fatalf("entry leaks token material: %s", raw)
	}
}

func TestListAuthFilesFromDiskExposesDisabledMetadataWhitelist(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	authDir := t.TempDir()
	fileName := "workbuddy-disabled.json"
	filePath := filepath.Join(authDir, fileName)
	content := `{"type":"workbuddy","disabled":true,"disabled_reason":"credits_exhausted","disabled_provider_code":"14018","disabled_at":"2026-09-16T00:00:00Z","access_token":"secret-token-value"}`
	if errWrite := os.WriteFile(filePath, []byte(content), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}
	// A file without disabled metadata must not emit the new keys.
	plainPath := filepath.Join(authDir, "plain.json")
	if errWrite := os.WriteFile(plainPath, []byte(`{"type":"codex"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)

	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret-token-value") {
		t.Fatalf("disk listing leaks token material: %s", rec.Body.String())
	}
	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("unmarshal response: %v", errUnmarshal)
	}
	var disabledEntry, plainEntry map[string]any
	for _, file := range payload.Files {
		switch file["name"] {
		case fileName:
			disabledEntry = file
		case "plain.json":
			plainEntry = file
		}
	}
	if disabledEntry == nil {
		t.Fatal("disabled auth file missing from listing")
	}
	if got := disabledEntry["disabled"]; got != true {
		t.Fatalf("disabled = %#v", got)
	}
	if got := disabledEntry["disabled_reason"]; got != "credits_exhausted" {
		t.Fatalf("disabled_reason = %#v", got)
	}
	if got := disabledEntry["disabled_provider_code"]; got != "14018" {
		t.Fatalf("disabled_provider_code = %#v", got)
	}
	if got := disabledEntry["disabled_at"]; got != "2026-09-16T00:00:00Z" {
		t.Fatalf("disabled_at = %#v", got)
	}
	if plainEntry == nil {
		t.Fatal("plain auth file missing from listing")
	}
	for _, key := range []string{"disabled_reason", "disabled_provider_code", "disabled_at"} {
		if _, exists := plainEntry[key]; exists {
			t.Fatalf("plain file gained %s: %#v", key, plainEntry[key])
		}
	}
}

func TestPatchAuthFileStatusReenableClearsAutoDisableState(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	manager := coreauth.NewManager(nil, nil, nil)
	if _, errRegister := manager.Register(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{
		ID:       "auth-wb",
		FileName: "workbuddy.json",
		Provider: "workbuddy",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"type": "workbuddy"},
	}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.MarkResult(context.Background(), coreauth.Result{
		AuthID:  "auth-wb",
		Model:   "m1",
		Success: false,
		Error: &coreauth.Error{
			Code:       coreauth.ErrorCodeCredentialCreditsExhausted,
			Message:    "WorkBuddy credits exhausted",
			HTTPStatus: 429,
		},
	})
	if auth, ok := manager.GetByID("auth-wb"); !ok || !auth.Disabled {
		t.Fatal("auth was not auto disabled")
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/status", strings.NewReader(`{"name":"workbuddy.json","disabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req

	h.PatchAuthFileStatus(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload map[string]any
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("unmarshal response: %v", errUnmarshal)
	}
	if payload["status"] != "ok" || payload["disabled"] != false {
		t.Fatalf("unexpected response payload: %v", payload)
	}

	auth, ok := manager.GetByID("auth-wb")
	if !ok {
		t.Fatal("auth not found")
	}
	if auth.Disabled || auth.Status != coreauth.StatusActive {
		t.Fatalf("expected re-enabled auth, got Disabled=%v Status=%v", auth.Disabled, auth.Status)
	}
	if auth.StatusMessage != "" {
		t.Fatalf("StatusMessage = %q, want empty", auth.StatusMessage)
	}
	if auth.LastError != nil {
		t.Fatalf("LastError = %#v, want nil", auth.LastError)
	}
	for _, key := range []string{coreauth.MetadataKeyDisabledReason, coreauth.MetadataKeyDisabledProviderCode, coreauth.MetadataKeyDisabledAt} {
		if _, exists := auth.Metadata[key]; exists {
			t.Fatalf("metadata key %s not cleared: %#v", key, auth.Metadata[key])
		}
	}
	if auth.Metadata["disabled"] != false {
		t.Fatalf("metadata disabled = %#v", auth.Metadata["disabled"])
	}
}
