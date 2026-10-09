package buddy

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func reserveSettings(floor float64) func() config.BuddyCreditsPatrolConfig {
	return func() config.BuddyCreditsPatrolConfig {
		cfg := testSettings()
		cfg.MinRemain = floor
		cfg.MinAccountInterval = time.Hour
		cfg.RequestTimeout = time.Second
		return cfg
	}
}

func newReserveGuard(t *testing.T, store ReserveStore, floor float64, fetch func(context.Context, *cliproxyauth.Auth) (float64, error)) *ReserveGuard {
	t.Helper()
	g := NewReserveGuard(ReserveGuardOptions{
		Store:      store,
		Settings:   reserveSettings(floor),
		FetchQuota: fetch,
		Now:        time.Now,
	})
	g.async = false
	return g
}

func TestReserveGuard_ParksBelowFloor(t *testing.T) {
	t.Parallel()
	auth := &cliproxyauth.Auth{
		ID:       "wb-" + uuid.NewString(),
		Provider: "workbuddy",
		Status:   cliproxyauth.StatusActive,
		Metadata: map[string]any{"type": "workbuddy", "access_token": "token"},
	}
	m := newPatrolManager(t, auth)
	var fetches atomic.Int32
	g := newReserveGuard(t, m, 10, func(context.Context, *cliproxyauth.Auth) (float64, error) {
		fetches.Add(1)
		return 9, nil
	})
	g.Observe(auth.ID, "workbuddy")
	g.Observe(auth.ID, "workbuddy")
	if fetches.Load() != 1 {
		t.Fatalf("fetches = %d, want 1", fetches.Load())
	}
	got, ok := m.GetByID(auth.ID)
	if !ok || !got.Disabled {
		t.Fatal("expected the auth to be parked")
	}
	if got.Metadata[cliproxyauth.MetadataKeyDisabledReason] != cliproxyauth.DisabledReasonCreditsReserve {
		t.Fatalf("reason = %#v", got.Metadata[cliproxyauth.MetadataKeyDisabledReason])
	}
}

func TestReserveGuard_FloorKeepsAccount(t *testing.T) {
	t.Parallel()
	auth := &cliproxyauth.Auth{
		ID:       "cb-" + uuid.NewString(),
		Provider: "codebuddy",
		Status:   cliproxyauth.StatusActive,
		Metadata: map[string]any{"type": "codebuddy"},
	}
	m := newPatrolManager(t, auth)
	g := newReserveGuard(t, m, 10, func(context.Context, *cliproxyauth.Auth) (float64, error) {
		return 10, nil
	})
	g.Observe(auth.ID, "CodeBuddy")
	got, _ := m.GetByID(auth.ID)
	if got.Disabled {
		t.Fatal("remain equal to the floor parked the auth")
	}
}

func TestReserveGuard_DefaultFloorSkipsFetch(t *testing.T) {
	t.Parallel()
	auth := &cliproxyauth.Auth{
		ID:       "wb-" + uuid.NewString(),
		Provider: "workbuddy",
		Metadata: map[string]any{"type": "workbuddy"},
	}
	m := newPatrolManager(t, auth)
	g := newReserveGuard(t, m, 1, func(context.Context, *cliproxyauth.Auth) (float64, error) {
		t.Fatal("default floor fetched quota")
		return 0, nil
	})
	g.Observe(auth.ID, "workbuddy")
	g.Observe(auth.ID, "gemini")
}

func TestRunRound_ConvertsPartialRemainToReserve(t *testing.T) {
	t.Parallel()
	wb := exhaustedBuddyAuth("workbuddy", "wb-"+uuid.NewString())
	m := newPatrolManager(t, wb)
	settings := reserveSettings(10)
	p := NewCreditsPatrol(CreditsPatrolOptions{
		Store:    m,
		Settings: settings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		FetchQuota: func(context.Context, *cliproxyauth.Auth) (float64, error) {
			return 9, nil
		},
	})
	p.runRound(context.Background(), settings())
	got, ok := m.GetByID(wb.ID)
	if !ok || !got.Disabled {
		t.Fatal("partial remain re-enabled the auth")
	}
	if got.Metadata[cliproxyauth.MetadataKeyDisabledReason] != cliproxyauth.DisabledReasonCreditsReserve {
		t.Fatalf("reason = %#v", got.Metadata[cliproxyauth.MetadataKeyDisabledReason])
	}
	if _, exists := got.Metadata[cliproxyauth.MetadataKeyDisabledProviderCode]; exists {
		t.Fatalf("provider code = %#v", got.Metadata[cliproxyauth.MetadataKeyDisabledProviderCode])
	}
	if got.StatusMessage != cliproxyauth.CreditsReserveStatusMessage("workbuddy") {
		t.Fatalf("status = %q", got.StatusMessage)
	}
}

func TestRunRound_ReenablesAtFloor(t *testing.T) {
	t.Parallel()
	wb := exhaustedBuddyAuth("workbuddy", "wb-"+uuid.NewString())
	wb.Metadata[cliproxyauth.MetadataKeyDisabledReason] = cliproxyauth.DisabledReasonCreditsReserve
	m := newPatrolManager(t, wb)
	settings := reserveSettings(10)
	p := NewCreditsPatrol(CreditsPatrolOptions{
		Store:    m,
		Settings: settings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		FetchQuota: func(context.Context, *cliproxyauth.Auth) (float64, error) {
			return 10, nil
		},
	})
	p.runRound(context.Background(), settings())
	got, _ := m.GetByID(wb.ID)
	if got.Disabled {
		t.Fatal("remain at the floor stayed disabled")
	}
}

func TestRunRound_ZeroRemainOnReserveBecomesExhausted(t *testing.T) {
	t.Parallel()
	wb := exhaustedBuddyAuth("workbuddy", "wb-"+uuid.NewString())
	wb.Metadata[cliproxyauth.MetadataKeyDisabledReason] = cliproxyauth.DisabledReasonCreditsReserve
	delete(wb.Metadata, cliproxyauth.MetadataKeyDisabledProviderCode)
	m := newPatrolManager(t, wb)
	settings := reserveSettings(10)
	p := NewCreditsPatrol(CreditsPatrolOptions{
		Store:    m,
		Settings: settings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		FetchQuota: func(context.Context, *cliproxyauth.Auth) (float64, error) {
			return 0, nil
		},
	})
	p.runRound(context.Background(), settings())
	got, _ := m.GetByID(wb.ID)
	if !got.Disabled {
		t.Fatal("zero remain re-enabled the auth")
	}
	if got.Metadata[cliproxyauth.MetadataKeyDisabledReason] != cliproxyauth.DisabledReasonCreditsExhausted {
		t.Fatalf("reason = %#v", got.Metadata[cliproxyauth.MetadataKeyDisabledReason])
	}
	if got.Metadata[cliproxyauth.MetadataKeyDisabledProviderCode] != "14018" {
		t.Fatalf("provider code = %#v", got.Metadata[cliproxyauth.MetadataKeyDisabledProviderCode])
	}
}
