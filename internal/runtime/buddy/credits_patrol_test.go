package buddy

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func exhaustedBuddyAuth(provider, id string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID:            id,
		Provider:      provider,
		Disabled:      true,
		Status:        cliproxyauth.StatusDisabled,
		StatusMessage: provider + " credits exhausted",
		Metadata: map[string]any{
			"type":                                 provider,
			"disabled":                             true,
			cliproxyauth.MetadataKeyDisabledReason: cliproxyauth.DisabledReasonCreditsExhausted,
			cliproxyauth.MetadataKeyDisabledProviderCode: "14018",
		},
	}
}

func newPatrolManager(t *testing.T, auths ...*cliproxyauth.Auth) *cliproxyauth.Manager {
	t.Helper()
	m := cliproxyauth.NewManager(nil, nil, nil)
	for _, auth := range auths {
		if _, err := m.Register(cliproxyauth.WithSkipPersist(context.Background()), auth); err != nil {
			t.Fatalf("Register(%s) error = %v", auth.ID, err)
		}
	}
	return m
}

func testSettings() config.BuddyCreditsPatrolConfig {
	cfg := config.DefaultBuddyCreditsPatrolConfig()
	cfg.StartupJitter = 0
	cfg.AccountJitter = 0
	cfg.MinAccountInterval = time.Second
	cfg.Interval = 12 * time.Hour
	cfg.MinRemain = 1
	return cfg
}

func TestSelectCandidates_OnlyCreditsExhaustedBuddy(t *testing.T) {
	t.Parallel()

	wb := exhaustedBuddyAuth("workbuddy", "wb-"+uuid.NewString())
	cb := exhaustedBuddyAuth("codebuddy", "cb-"+uuid.NewString())
	manual := &cliproxyauth.Auth{
		ID:       "manual-" + uuid.NewString(),
		Provider: "workbuddy",
		Disabled: true,
		Status:   cliproxyauth.StatusDisabled,
		Metadata: map[string]any{"type": "workbuddy", "disabled": true},
	}
	active := &cliproxyauth.Auth{
		ID:       "active-" + uuid.NewString(),
		Provider: "workbuddy",
		Metadata: map[string]any{"type": "workbuddy"},
	}
	other := exhaustedBuddyAuth("gemini", "gem-"+uuid.NewString())
	recent := exhaustedBuddyAuth("workbuddy", "recent-"+uuid.NewString())
	recent.Metadata[cliproxyauth.MetadataKeyCreditsPatrolAt] = time.Now().UTC().Format(time.RFC3339Nano)

	m := newPatrolManager(t, wb, cb, manual, active, other, recent)
	p := NewCreditsPatrol(CreditsPatrolOptions{
		Store:    m,
		Settings: testSettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
	})

	got := p.selectCandidates(time.Now(), testSettings())
	ids := map[string]struct{}{}
	for _, auth := range got {
		ids[auth.ID] = struct{}{}
	}
	if _, ok := ids[wb.ID]; !ok {
		t.Fatalf("missing workbuddy candidate")
	}
	if _, ok := ids[cb.ID]; !ok {
		t.Fatalf("missing codebuddy candidate")
	}
	for _, id := range []string{manual.ID, active.ID, other.ID, recent.ID} {
		if _, ok := ids[id]; ok {
			t.Fatalf("unexpected candidate %s", id)
		}
	}
}

func TestRunRound_ReenablesWhenCreditsRemain(t *testing.T) {
	t.Parallel()

	wb := exhaustedBuddyAuth("workbuddy", "wb-"+uuid.NewString())
	m := newPatrolManager(t, wb)
	p := NewCreditsPatrol(CreditsPatrolOptions{
		Store:    m,
		Settings: testSettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		FetchQuota: func(context.Context, *cliproxyauth.Auth) (float64, error) {
			return 100, nil
		},
	})

	p.runRound(context.Background(), testSettings())

	got, ok := m.GetByID(wb.ID)
	if !ok {
		t.Fatal("auth missing")
	}
	if got.Disabled || got.Status != cliproxyauth.StatusActive {
		t.Fatalf("expected re-enabled auth, Disabled=%v Status=%v", got.Disabled, got.Status)
	}
	if got.StatusMessage != "" {
		t.Fatalf("StatusMessage = %q", got.StatusMessage)
	}
	if _, exists := got.Metadata[cliproxyauth.MetadataKeyDisabledReason]; exists {
		t.Fatalf("disabled_reason not cleared: %#v", got.Metadata[cliproxyauth.MetadataKeyDisabledReason])
	}
	if got.Metadata[cliproxyauth.MetadataKeyCreditsPatrolResult] != cliproxyauth.CreditsPatrolResultReenabled {
		t.Fatalf("patrol result = %#v", got.Metadata[cliproxyauth.MetadataKeyCreditsPatrolResult])
	}
	if _, ok := got.Metadata[cliproxyauth.MetadataKeyCreditsPatrolAt]; !ok {
		t.Fatal("missing credits_patrol_at after re-enable")
	}
}

func TestRunRound_KeepsDisabledWhenEmpty(t *testing.T) {
	t.Parallel()

	wb := exhaustedBuddyAuth("workbuddy", "wb-"+uuid.NewString())
	m := newPatrolManager(t, wb)
	p := NewCreditsPatrol(CreditsPatrolOptions{
		Store:    m,
		Settings: testSettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		FetchQuota: func(context.Context, *cliproxyauth.Auth) (float64, error) {
			return 0, nil
		},
	})

	p.runRound(context.Background(), testSettings())

	got, ok := m.GetByID(wb.ID)
	if !ok {
		t.Fatal("auth missing")
	}
	if !got.Disabled {
		t.Fatal("empty credits re-enabled the auth")
	}
	if got.Metadata[cliproxyauth.MetadataKeyCreditsPatrolResult] != cliproxyauth.CreditsPatrolResultStillEmpty {
		t.Fatalf("patrol result = %#v", got.Metadata[cliproxyauth.MetadataKeyCreditsPatrolResult])
	}
	if _, ok := got.Metadata[cliproxyauth.MetadataKeyCreditsPatrolAt]; !ok {
		t.Fatal("missing credits_patrol_at")
	}
}

func TestRunRound_AuthInvalidKeepsDisabled(t *testing.T) {
	t.Parallel()

	wb := exhaustedBuddyAuth("workbuddy", "wb-"+uuid.NewString())
	m := newPatrolManager(t, wb)
	p := NewCreditsPatrol(CreditsPatrolOptions{
		Store:    m,
		Settings: testSettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		FetchQuota: func(context.Context, *cliproxyauth.Auth) (float64, error) {
			return 0, fmt.Errorf("workbuddy: resource summary status 401")
		},
	})

	p.runRound(context.Background(), testSettings())

	got, ok := m.GetByID(wb.ID)
	if !ok {
		t.Fatal("auth missing")
	}
	if !got.Disabled {
		t.Fatal("invalid credential was re-enabled")
	}
	if got.Metadata[cliproxyauth.MetadataKeyDisabledReason] != cliproxyauth.DisabledReasonCreditsExhausted {
		t.Fatalf("disabled_reason = %#v", got.Metadata[cliproxyauth.MetadataKeyDisabledReason])
	}
	if got.Metadata[cliproxyauth.MetadataKeyCreditsPatrolResult] != cliproxyauth.CreditsPatrolResultAuthInvalid {
		t.Fatalf("patrol result = %#v", got.Metadata[cliproxyauth.MetadataKeyCreditsPatrolResult])
	}
}

func TestRunRound_TransientAbortsRemaining(t *testing.T) {
	t.Parallel()

	first := exhaustedBuddyAuth("workbuddy", "wb-a-"+uuid.NewString())
	second := exhaustedBuddyAuth("codebuddy", "cb-b-"+uuid.NewString())
	m := newPatrolManager(t, first, second)

	var mu sync.Mutex
	var fetched []string
	p := NewCreditsPatrol(CreditsPatrolOptions{
		Store:    m,
		Settings: testSettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		FetchQuota: func(_ context.Context, auth *cliproxyauth.Auth) (float64, error) {
			mu.Lock()
			fetched = append(fetched, auth.ID)
			mu.Unlock()
			return 0, fmt.Errorf("workbuddy: resource summary status %d", http.StatusGatewayTimeout)
		},
	})

	p.runRound(context.Background(), testSettings())

	mu.Lock()
	defer mu.Unlock()
	if len(fetched) != 1 {
		t.Fatalf("fetched %v, want exactly one auth before abort", fetched)
	}
	secondGot, _ := m.GetByID(second.ID)
	if secondGot.Metadata[cliproxyauth.MetadataKeyCreditsPatrolAt] != nil {
		t.Fatalf("unprocessed auth was stamped: %#v", secondGot.Metadata)
	}
}

func TestRunRound_WaitsBetweenAccounts(t *testing.T) {
	t.Parallel()

	a := exhaustedBuddyAuth("workbuddy", "wb-a-"+uuid.NewString())
	b := exhaustedBuddyAuth("codebuddy", "cb-b-"+uuid.NewString())
	m := newPatrolManager(t, a, b)

	var sleeps []time.Duration
	p := NewCreditsPatrol(CreditsPatrolOptions{
		Store:    m,
		Settings: testSettings,
		Intn:     func(int) int { return 0 },
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
		FetchQuota: func(context.Context, *cliproxyauth.Auth) (float64, error) {
			return 0, nil
		},
	})

	p.runRound(context.Background(), testSettings())

	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Fatalf("sleeps = %v, want one min-account-interval", sleeps)
	}
}

func TestClassifyQuotaError(t *testing.T) {
	t.Parallel()

	if got := classifyQuotaError(fmt.Errorf("workbuddy: resource status 504")); got != "transient" {
		t.Fatalf("504 class = %q", got)
	}
	if got := classifyQuotaError(fmt.Errorf("codebuddy: resource status 429")); got != "transient" {
		t.Fatalf("429 class = %q", got)
	}
	if got := classifyQuotaError(fmt.Errorf("workbuddy: resource summary status 401")); got != "auth_invalid" {
		t.Fatalf("401 class = %q", got)
	}
	if got := classifyQuotaError(context.DeadlineExceeded); got != "transient" {
		t.Fatalf("deadline class = %q", got)
	}
}

func TestIsCreditsPatrolCandidate_PluginVirtualExcluded(t *testing.T) {
	t.Parallel()

	auth := exhaustedBuddyAuth("workbuddy", "virt-"+uuid.NewString())
	cliproxyauth.MarkPluginVirtualAuth(auth, "/tmp/source.json", 1)
	if isCreditsPatrolCandidate(auth, time.Now(), time.Hour) {
		t.Fatal("plugin virtual auth must not be patrolled")
	}
}
