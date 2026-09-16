package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type terminalDisableStore struct {
	mu      sync.Mutex
	saved   []*Auth
	saveErr error
}

func (s *terminalDisableStore) List(context.Context) ([]*Auth, error) {
	return nil, nil
}

func (s *terminalDisableStore) Save(_ context.Context, auth *Auth) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return "", s.saveErr
	}
	s.saved = append(s.saved, auth.Clone())
	return auth.ID, nil
}

func (s *terminalDisableStore) Delete(context.Context, string) error {
	return nil
}

func (s *terminalDisableStore) lastSaved() *Auth {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saved) == 0 {
		return nil
	}
	return s.saved[len(s.saved)-1]
}

func creditsExhaustedResult(authID, model string) Result {
	return Result{
		AuthID:          authID,
		Model:           model,
		Success:         false,
		CredentialScope: true,
		Error: &Error{
			Code:       ErrorCodeCredentialCreditsExhausted,
			Message:    "WorkBuddy credits exhausted",
			Retryable:  false,
			HTTPStatus: 429,
		},
	}
}

func TestManager_MarkResult_CreditsExhaustedDisablesAuth(t *testing.T) {
	t.Parallel()

	store := &terminalDisableStore{}
	m := NewManager(store, nil, nil)
	authID := "wb-" + uuid.NewString()
	auth := &Auth{
		ID:       authID,
		Provider: "workbuddy",
		Metadata: map[string]any{"type": "workbuddy"},
		ModelStates: map[string]*ModelState{
			"m1": {Status: StatusError, Unavailable: true, StatusMessage: "quota", NextRetryAfter: time.Now().Add(time.Hour), LastError: &Error{Code: "quota"}, Quota: QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: time.Now().Add(time.Hour)}},
			"m2": {Status: StatusError, Unavailable: true},
		},
	}
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	m.MarkResult(context.Background(), creditsExhaustedResult(authID, "m1"))

	got, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	if !got.Disabled || got.Status != StatusDisabled {
		t.Fatalf("expected disabled auth, got Disabled=%v Status=%v", got.Disabled, got.Status)
	}
	if got.StatusMessage != "WorkBuddy credits exhausted" {
		t.Fatalf("StatusMessage = %q", got.StatusMessage)
	}
	if got.Unavailable || !got.NextRetryAfter.IsZero() {
		t.Fatalf("residual cooldown state: Unavailable=%v NextRetryAfter=%v", got.Unavailable, got.NextRetryAfter)
	}
	if got.Quota.Exceeded || got.Quota.Reason != "" {
		t.Fatalf("residual quota state: %#v", got.Quota)
	}
	if got.LastError == nil || got.LastError.Code != ErrorCodeCredentialCreditsExhausted {
		t.Fatalf("LastError = %#v", got.LastError)
	}
	for model, state := range got.ModelStates {
		if !modelStateIsClean(state) {
			t.Fatalf("model %s not clean: %#v", model, state)
		}
	}
	if got.Metadata["disabled"] != true {
		t.Fatalf("metadata disabled = %#v", got.Metadata["disabled"])
	}
	if got.Metadata[MetadataKeyDisabledReason] != DisabledReasonCreditsExhausted {
		t.Fatalf("disabled_reason = %#v", got.Metadata[MetadataKeyDisabledReason])
	}
	if got.Metadata[MetadataKeyDisabledProviderCode] != "14018" {
		t.Fatalf("disabled_provider_code = %#v", got.Metadata[MetadataKeyDisabledProviderCode])
	}
	disabledAt, ok := got.Metadata[MetadataKeyDisabledAt].(string)
	if !ok || disabledAt == "" {
		t.Fatalf("disabled_at = %#v", got.Metadata[MetadataKeyDisabledAt])
	}
	if _, errParse := time.Parse(time.RFC3339Nano, disabledAt); errParse != nil {
		t.Fatalf("disabled_at not RFC3339: %v", errParse)
	}
}

func TestManager_MarkResult_CreditsExhaustedDisablesDespiteDisableCooling(t *testing.T) {
	t.Parallel()

	m := NewManager(nil, nil, nil)
	authID := "wb-" + uuid.NewString()
	auth := &Auth{
		ID:       authID,
		Provider: "workbuddy",
		Metadata: map[string]any{"type": "workbuddy", "disable_cooling": true},
	}
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	m.MarkResult(context.Background(), creditsExhaustedResult(authID, "m1"))

	got, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	if !got.Disabled || got.Status != StatusDisabled {
		t.Fatalf("disable_cooling must not bypass terminal disable, got Disabled=%v Status=%v", got.Disabled, got.Status)
	}
}

func TestManager_MarkResult_CreditsExhaustedKeepsFirstDisabledAt(t *testing.T) {
	t.Parallel()

	m := NewManager(nil, nil, nil)
	authID := "wb-" + uuid.NewString()
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       authID,
		Provider: "workbuddy",
		Metadata: map[string]any{"type": "workbuddy"},
	}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	m.MarkResult(context.Background(), creditsExhaustedResult(authID, "m1"))
	first, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	firstAt := first.Metadata[MetadataKeyDisabledAt]

	m.MarkResult(context.Background(), creditsExhaustedResult(authID, "m2"))
	second, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	if second.Metadata[MetadataKeyDisabledAt] != firstAt {
		t.Fatalf("disabled_at drifted: %v -> %v", firstAt, second.Metadata[MetadataKeyDisabledAt])
	}
	if !second.Disabled {
		t.Fatal("auth lost disabled state")
	}
}

func TestManager_MarkResult_CreditsExhaustedConcurrent(t *testing.T) {
	t.Parallel()

	m := NewManager(nil, nil, nil)
	authID := "wb-" + uuid.NewString()
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       authID,
		Provider: "workbuddy",
		Metadata: map[string]any{"type": "workbuddy"},
	}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.MarkResult(context.Background(), creditsExhaustedResult(authID, "m1"))
		}()
	}
	wg.Wait()

	got, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	if !got.Disabled || got.Status != StatusDisabled {
		t.Fatalf("expected disabled auth, got Disabled=%v Status=%v", got.Disabled, got.Status)
	}
	if got.Metadata[MetadataKeyDisabledReason] != DisabledReasonCreditsExhausted {
		t.Fatalf("disabled_reason = %#v", got.Metadata[MetadataKeyDisabledReason])
	}
	if disabledAt, ok := got.Metadata[MetadataKeyDisabledAt].(string); !ok || disabledAt == "" {
		t.Fatalf("disabled_at = %#v", got.Metadata[MetadataKeyDisabledAt])
	}
}

func TestManager_MarkResult_CreditsExhaustedManualDisableWins(t *testing.T) {
	t.Parallel()

	m := NewManager(nil, nil, nil)
	authID := "wb-" + uuid.NewString()
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
		ID:            authID,
		Provider:      "workbuddy",
		Disabled:      true,
		Status:        StatusDisabled,
		StatusMessage: "disabled via management API",
		Metadata:      map[string]any{"type": "workbuddy", "disabled": true},
	}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	m.MarkResult(context.Background(), creditsExhaustedResult(authID, "m1"))

	got, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	if got.StatusMessage != "disabled via management API" {
		t.Fatalf("manual StatusMessage overwritten: %q", got.StatusMessage)
	}
	if _, exists := got.Metadata[MetadataKeyDisabledReason]; exists {
		t.Fatalf("manual disable gained auto reason: %#v", got.Metadata[MetadataKeyDisabledReason])
	}
	if _, exists := got.Metadata[MetadataKeyDisabledAt]; exists {
		t.Fatalf("manual disable gained disabled_at: %#v", got.Metadata[MetadataKeyDisabledAt])
	}
}

func TestManager_MarkResult_CreditsExhaustedPersistsMetadata(t *testing.T) {
	t.Parallel()

	store := &terminalDisableStore{}
	m := NewManager(store, nil, nil)
	authID := "wb-" + uuid.NewString()
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       authID,
		Provider: "codebuddy",
		Metadata: map[string]any{"type": "codebuddy"},
	}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	result := creditsExhaustedResult(authID, "m1")
	result.Error.Message = "CodeBuddy credits exhausted"
	m.MarkResult(context.Background(), result)

	saved := store.lastSaved()
	if saved == nil {
		t.Fatal("store.Save was not called")
	}
	if saved.Metadata["disabled"] != true {
		t.Fatalf("persisted disabled = %#v", saved.Metadata["disabled"])
	}
	if saved.Metadata[MetadataKeyDisabledReason] != DisabledReasonCreditsExhausted {
		t.Fatalf("persisted disabled_reason = %#v", saved.Metadata[MetadataKeyDisabledReason])
	}
	if saved.Metadata[MetadataKeyDisabledProviderCode] != "14018" {
		t.Fatalf("persisted disabled_provider_code = %#v", saved.Metadata[MetadataKeyDisabledProviderCode])
	}
	if _, ok := saved.Metadata[MetadataKeyDisabledAt].(string); !ok {
		t.Fatalf("persisted disabled_at = %#v", saved.Metadata[MetadataKeyDisabledAt])
	}
}

func TestManager_MarkResult_CreditsExhaustedPersistFailureKeepsDisabled(t *testing.T) {
	t.Parallel()

	store := &terminalDisableStore{saveErr: errors.New("disk full")}
	m := NewManager(store, nil, nil)
	authID := "wb-" + uuid.NewString()
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       authID,
		Provider: "workbuddy",
		Metadata: map[string]any{"type": "workbuddy"},
	}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	m.MarkResult(context.Background(), creditsExhaustedResult(authID, "m1"))

	got, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	if !got.Disabled || got.Status != StatusDisabled {
		t.Fatalf("runtime state lost disabled after persist failure: Disabled=%v Status=%v", got.Disabled, got.Status)
	}
}

func TestManager_MarkResult_NonBuddyProviderCreditsExhausted(t *testing.T) {
	t.Parallel()

	m := NewManager(nil, nil, nil)
	authID := "gen-" + uuid.NewString()
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       authID,
		Provider: "gemini",
		Metadata: map[string]any{"type": "gemini"},
	}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	m.MarkResult(context.Background(), creditsExhaustedResult(authID, "m1"))

	got, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	if !got.Disabled {
		t.Fatal("credits exhausted did not disable")
	}
	if _, exists := got.Metadata[MetadataKeyDisabledProviderCode]; exists {
		t.Fatalf("non-Buddy provider gained provider code: %#v", got.Metadata[MetadataKeyDisabledProviderCode])
	}
}

func TestManager_MarkResult_GatewayTimeoutDoesNotDisable(t *testing.T) {
	t.Parallel()

	m := NewManager(nil, nil, nil)
	authID := "wb-" + uuid.NewString()
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       authID,
		Provider: "workbuddy",
		Metadata: map[string]any{"type": "workbuddy"},
	}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	m.MarkResult(context.Background(), Result{
		AuthID:  authID,
		Model:   "m1",
		Success: false,
		Error: &Error{
			Code:       ErrorCodeUpstreamGatewayTimeout,
			Message:    "WorkBuddy upstream gateway timed out",
			Retryable:  true,
			HTTPStatus: 504,
		},
	})

	got, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	if got.Disabled || got.Status == StatusDisabled {
		t.Fatalf("504 disabled the auth: Disabled=%v Status=%v", got.Disabled, got.Status)
	}
	if _, exists := got.Metadata[MetadataKeyDisabledReason]; exists {
		t.Fatal("504 wrote disabled metadata")
	}
}

func TestManager_RegisterNormalizesPersistedDisabledMetadata(t *testing.T) {
	t.Parallel()

	m := NewManager(nil, nil, nil)
	authID := "wb-" + uuid.NewString()
	registered, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
		ID:             authID,
		Provider:       "workbuddy",
		Unavailable:    true,
		NextRetryAfter: time.Now().Add(time.Hour),
		Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: time.Now().Add(time.Hour)},
		ModelStates: map[string]*ModelState{
			"m1": {Status: StatusError, Unavailable: true, StatusMessage: "quota", NextRetryAfter: time.Now().Add(time.Hour), LastError: &Error{Code: "quota"}},
		},
		Metadata: map[string]any{
			"type":                          "workbuddy",
			"disabled":                      true,
			MetadataKeyDisabledReason:       DisabledReasonCreditsExhausted,
			MetadataKeyDisabledProviderCode: "14018",
			MetadataKeyDisabledAt:           "2026-09-16T00:00:00Z",
		},
	})
	if errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	if registered == nil || !registered.Disabled || registered.Status != StatusDisabled {
		t.Fatalf("expected disabled auth, got %#v", registered)
	}
	if registered.StatusMessage != "WorkBuddy credits exhausted" {
		t.Fatalf("StatusMessage = %q", registered.StatusMessage)
	}
	if registered.Unavailable || !registered.NextRetryAfter.IsZero() || registered.Quota.Exceeded {
		t.Fatalf("residual transient state kept: %#v", registered)
	}
	for model, state := range registered.ModelStates {
		if !modelStateIsClean(state) {
			t.Fatalf("model %s not clean: %#v", model, state)
		}
	}
}

type loadDisabledStore struct {
	auths []*Auth
}

func (s *loadDisabledStore) List(context.Context) ([]*Auth, error) {
	return s.auths, nil
}

func (s *loadDisabledStore) Save(context.Context, *Auth) (string, error) {
	return "", nil
}

func (s *loadDisabledStore) Delete(context.Context, string) error {
	return nil
}

func TestManager_LoadRestoresDisabledMetadata(t *testing.T) {
	t.Parallel()

	store := &loadDisabledStore{auths: []*Auth{
		{
			ID:             "wb-" + uuid.NewString(),
			Provider:       "codebuddy",
			Unavailable:    true,
			NextRetryAfter: time.Now().Add(time.Hour),
			Quota:          QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: time.Now().Add(time.Hour)},
			ModelStates: map[string]*ModelState{
				"m1": {Status: StatusError, Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour)},
			},
			Metadata: map[string]any{
				"type":                          "codebuddy",
				"disabled":                      true,
				MetadataKeyDisabledReason:       DisabledReasonCreditsExhausted,
				MetadataKeyDisabledProviderCode: "14018",
				MetadataKeyDisabledAt:           "2026-09-16T00:00:00Z",
			},
		},
		{
			ID:            "legacy-" + uuid.NewString(),
			Provider:      "gemini",
			StatusMessage: "manual op note",
			Metadata:      map[string]any{"type": "gemini", "disabled": true},
		},
		{
			ID:       "unknown-" + uuid.NewString(),
			Provider: "claude",
			Metadata: map[string]any{
				"type":                    "claude",
				"disabled":                true,
				MetadataKeyDisabledReason: "totally <html>unknown</html>",
			},
		},
	}}

	m := NewManager(store, nil, nil)
	if errLoad := m.Load(context.Background()); errLoad != nil {
		t.Fatalf("Load() error = %v", errLoad)
	}

	credits, ok := m.GetByID(store.auths[0].ID)
	if !ok {
		t.Fatal("credits auth not loaded")
	}
	if !credits.Disabled || credits.Status != StatusDisabled {
		t.Fatalf("expected disabled auth, got Disabled=%v Status=%v", credits.Disabled, credits.Status)
	}
	if credits.StatusMessage != "CodeBuddy credits exhausted" {
		t.Fatalf("StatusMessage = %q", credits.StatusMessage)
	}
	if credits.Unavailable || !credits.NextRetryAfter.IsZero() || credits.Quota.Exceeded {
		t.Fatalf("residual transient state kept: %#v", credits)
	}
	for model, state := range credits.ModelStates {
		if !modelStateIsClean(state) {
			t.Fatalf("model %s not clean: %#v", model, state)
		}
	}

	legacy, ok := m.GetByID(store.auths[1].ID)
	if !ok {
		t.Fatal("legacy auth not loaded")
	}
	if !legacy.Disabled || legacy.Status != StatusDisabled {
		t.Fatalf("legacy disabled auth lost status: %#v", legacy)
	}
	if legacy.StatusMessage != "manual op note" {
		t.Fatalf("manual message overwritten: %q", legacy.StatusMessage)
	}

	unknown, ok := m.GetByID(store.auths[2].ID)
	if !ok {
		t.Fatal("unknown-reason auth not loaded")
	}
	if !unknown.Disabled || unknown.Status != StatusDisabled {
		t.Fatalf("unknown-reason auth lost status: %#v", unknown)
	}
	if unknown.StatusMessage == "totally <html>unknown</html>" {
		t.Fatal("unknown metadata reason leaked into StatusMessage")
	}
}

func TestManager_Update_ReenableClearsAutoDisableState(t *testing.T) {
	t.Parallel()

	store := &terminalDisableStore{}
	m := NewManager(store, nil, nil)
	authID := "wb-" + uuid.NewString()
	model := "wb-model-" + uuid.NewString()
	registry.GetGlobalRegistry().RegisterClient(authID, "workbuddy", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(authID)
	})
	if _, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       authID,
		Provider: "workbuddy",
		Metadata: map[string]any{"type": "workbuddy"},
	}); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}

	m.MarkResult(context.Background(), creditsExhaustedResult(authID, model))
	disabled, ok := m.GetByID(authID)
	if !ok || !disabled.Disabled {
		t.Fatal("auth was not auto disabled")
	}
	registry.GetGlobalRegistry().SuspendClientModel(authID, model, "quota")

	reenable := disabled.Clone()
	reenable.Disabled = false
	reenable.Status = StatusActive
	reenable.StatusMessage = ""
	reenable.Metadata["disabled"] = false
	delete(reenable.Metadata, MetadataKeyDisabledReason)
	delete(reenable.Metadata, MetadataKeyDisabledProviderCode)
	delete(reenable.Metadata, MetadataKeyDisabledAt)

	if _, errUpdate := m.Update(context.Background(), reenable); errUpdate != nil {
		t.Fatalf("Update() error = %v", errUpdate)
	}

	got, ok := m.GetByID(authID)
	if !ok {
		t.Fatal("auth not found")
	}
	if got.Disabled || got.Status != StatusActive {
		t.Fatalf("expected enabled active auth, got Disabled=%v Status=%v", got.Disabled, got.Status)
	}
	if got.StatusMessage != "" {
		t.Fatalf("StatusMessage = %q, want empty", got.StatusMessage)
	}
	if got.LastError != nil {
		t.Fatalf("LastError = %#v, want nil", got.LastError)
	}
	for _, key := range []string{MetadataKeyDisabledReason, MetadataKeyDisabledProviderCode, MetadataKeyDisabledAt} {
		if _, exists := got.Metadata[key]; exists {
			t.Fatalf("metadata key %s not cleared: %#v", key, got.Metadata[key])
		}
	}
	if reason := registry.GetGlobalRegistry().GetClientModelSuspensionReason(authID, model); reason != "" {
		t.Fatalf("model suspension not resumed: %q", reason)
	}

	saved := store.lastSaved()
	if saved == nil {
		t.Fatal("store.Save was not called for update")
	}
	for _, key := range []string{MetadataKeyDisabledReason, MetadataKeyDisabledProviderCode, MetadataKeyDisabledAt} {
		if _, exists := saved.Metadata[key]; exists {
			t.Fatalf("persisted metadata key %s not cleared: %#v", key, saved.Metadata[key])
		}
	}
	if saved.Metadata["disabled"] != false {
		t.Fatalf("persisted disabled = %#v", saved.Metadata["disabled"])
	}
}

type terminalFailoverExecutor struct {
	badID string
	mu    sync.Mutex
	calls []string
}

func (e *terminalFailoverExecutor) Identifier() string { return "workbuddy" }

func (e *terminalFailoverExecutor) Execute(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.mu.Lock()
	e.calls = append(e.calls, auth.ID)
	e.mu.Unlock()
	if auth.ID == e.badID {
		return cliproxyexecutor.Response{}, &Error{
			Code:       ErrorCodeCredentialCreditsExhausted,
			Message:    "WorkBuddy credits exhausted",
			Retryable:  false,
			HTTPStatus: 429,
		}
	}
	return cliproxyexecutor.Response{Payload: []byte("ok")}, nil
}

func (e *terminalFailoverExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, errors.New("not implemented")
}

func (e *terminalFailoverExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *terminalFailoverExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{Payload: []byte("ok")}, nil
}

func (e *terminalFailoverExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

// terminalFailoverSelector deterministically picks the bad credential first when
// it is still a candidate, otherwise the first remaining auth.
type terminalFailoverSelector struct {
	preferredID string
}

func (s *terminalFailoverSelector) Pick(_ context.Context, _, _ string, _ cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	for _, candidate := range auths {
		if candidate != nil && candidate.ID == s.preferredID {
			return candidate, nil
		}
	}
	for _, candidate := range auths {
		if candidate != nil {
			return candidate, nil
		}
	}
	return nil, &Error{Code: "auth_not_found", Message: "no auth available"}
}

func TestManager_Execute_CreditsExhaustedDisablesAndFailsOver(t *testing.T) {
	t.Parallel()

	badID := "wb-bad-" + uuid.NewString()
	goodID := "wb-good-" + uuid.NewString()
	model := "wb-exec-" + uuid.NewString()

	executor := &terminalFailoverExecutor{badID: badID}
	m := NewManager(nil, &terminalFailoverSelector{preferredID: badID}, nil)
	m.SetRetryConfig(0, 0, 0)
	m.RegisterExecutor(executor)

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(badID, "workbuddy", []*registry.ModelInfo{{ID: model}})
	reg.RegisterClient(goodID, "workbuddy", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient(badID)
		reg.UnregisterClient(goodID)
	})

	for _, id := range []string{badID, goodID} {
		if _, errRegister := m.Register(WithSkipPersist(context.Background()), &Auth{
			ID:       id,
			Provider: "workbuddy",
			Metadata: map[string]any{"type": "workbuddy"},
		}); errRegister != nil {
			t.Fatalf("Register(%s) error = %v", id, errRegister)
		}
	}

	resp, errExecute := m.Execute(context.Background(), []string{"workbuddy"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}
	if string(resp.Payload) != "ok" {
		t.Fatalf("payload = %q, want ok", resp.Payload)
	}

	executor.mu.Lock()
	calls := append([]string(nil), executor.calls...)
	executor.mu.Unlock()
	if len(calls) != 2 || calls[0] != badID || calls[1] != goodID {
		t.Fatalf("call order = %v, want [%s %s]", calls, badID, goodID)
	}

	bad, ok := m.GetByID(badID)
	if !ok {
		t.Fatal("bad auth not found")
	}
	if !bad.Disabled || bad.Status != StatusDisabled {
		t.Fatalf("bad auth not terminally disabled: Disabled=%v Status=%v", bad.Disabled, bad.Status)
	}
	if bad.Metadata[MetadataKeyDisabledReason] != DisabledReasonCreditsExhausted {
		t.Fatalf("bad auth disabled_reason = %#v", bad.Metadata[MetadataKeyDisabledReason])
	}

	good, ok := m.GetByID(goodID)
	if !ok {
		t.Fatal("good auth not found")
	}
	if good.Disabled || good.Status == StatusDisabled {
		t.Fatalf("good auth was disabled: Disabled=%v Status=%v", good.Disabled, good.Status)
	}
}
