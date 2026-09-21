package buddy

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func globalCodeBuddyAuth(id string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID:       id,
		Provider: "codebuddy",
		Status:   cliproxyauth.StatusActive,
		Metadata: map[string]any{
			"type":         "codebuddy",
			"access_token": "token",
			"user_id":      "user-1",
			"domain":       codebuddy.DefaultDomainGlobal,
		},
	}
}

func activitySettings() config.BuddyActivityPatrolConfig {
	cfg := config.DefaultBuddyActivityPatrolConfig()
	cfg.StartupJitter = 0
	cfg.AccountJitter = 0
	cfg.MinAccountInterval = time.Second
	cfg.Interval = 24 * time.Hour
	return cfg
}

func TestSelectActivityCandidates_OnlyGlobalCodeBuddy(t *testing.T) {
	t.Parallel()

	ok := globalCodeBuddyAuth("cb-" + uuid.NewString())
	cn := globalCodeBuddyAuth("cn-" + uuid.NewString())
	cn.Metadata["domain"] = codebuddy.DefaultDomain
	wb := globalCodeBuddyAuth("wb-" + uuid.NewString())
	wb.Provider = "workbuddy"
	wb.Metadata["type"] = "workbuddy"
	wb.Metadata["domain"] = "www.workbuddy.ai"
	disabled := globalCodeBuddyAuth("dis-" + uuid.NewString())
	disabled.Disabled = true
	disabled.Status = cliproxyauth.StatusDisabled
	recent := globalCodeBuddyAuth("recent-" + uuid.NewString())
	recent.Metadata[cliproxyauth.MetadataKeyActivityPatrolAt] = time.Now().UTC().Format(time.RFC3339Nano)
	noToken := globalCodeBuddyAuth("notoken-" + uuid.NewString())
	delete(noToken.Metadata, "access_token")

	m := newPatrolManager(t, ok, cn, wb, disabled, recent, noToken)
	p := NewActivityPatrol(ActivityPatrolOptions{
		Store:    m,
		Settings: activitySettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
	})

	got := p.selectCandidates(time.Now(), activitySettings())
	if len(got) != 1 || got[0].ID != ok.ID {
		ids := make([]string, 0, len(got))
		for _, auth := range got {
			ids = append(ids, auth.ID)
		}
		t.Fatalf("candidates = %v, want [%s]", ids, ok.ID)
	}
}

func TestSelectActivityCandidates_PluginVirtualExcluded(t *testing.T) {
	t.Parallel()

	auth := globalCodeBuddyAuth("virt-" + uuid.NewString())
	cliproxyauth.MarkPluginVirtualAuth(auth, "/tmp/source.json", 1)
	if isActivityPatrolCandidate(auth, time.Now(), time.Hour) {
		t.Fatal("plugin virtual auth must not be patrolled")
	}
}

func TestActivityRunRound_StampsSuccess(t *testing.T) {
	t.Parallel()

	auth := globalCodeBuddyAuth("cb-" + uuid.NewString())
	m := newPatrolManager(t, auth)
	p := NewActivityPatrol(ActivityPatrolOptions{
		Store:    m,
		Settings: activitySettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		Ping: func(_ context.Context, a *cliproxyauth.Auth, model string) error {
			if model != config.DefaultBuddyActivityPatrolModel {
				t.Fatalf("model = %q", model)
			}
			a.Metadata[cliproxyauth.MetadataKeyCLIMachineID] = "machine-1"
			return nil
		},
	})

	p.runRound(context.Background(), activitySettings())

	got, ok := m.GetByID(auth.ID)
	if !ok {
		t.Fatal("auth missing")
	}
	if got.Metadata[cliproxyauth.MetadataKeyActivityPatrolResult] != cliproxyauth.ActivityPatrolResultOK {
		t.Fatalf("result = %#v", got.Metadata[cliproxyauth.MetadataKeyActivityPatrolResult])
	}
	if _, exists := got.Metadata[cliproxyauth.MetadataKeyActivityPatrolAt]; !exists {
		t.Fatal("missing activity_patrol_at")
	}
	if got.Metadata[cliproxyauth.MetadataKeyCLIMachineID] != "machine-1" {
		t.Fatalf("machine id = %#v", got.Metadata[cliproxyauth.MetadataKeyCLIMachineID])
	}
}

func TestActivityRunRound_TransientAbortsRemaining(t *testing.T) {
	t.Parallel()

	first := globalCodeBuddyAuth("cb-a-" + uuid.NewString())
	second := globalCodeBuddyAuth("cb-b-" + uuid.NewString())
	m := newPatrolManager(t, first, second)

	var mu sync.Mutex
	var pinged []string
	p := NewActivityPatrol(ActivityPatrolOptions{
		Store:    m,
		Settings: activitySettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		Ping: func(_ context.Context, auth *cliproxyauth.Auth, _ string) error {
			mu.Lock()
			pinged = append(pinged, auth.ID)
			mu.Unlock()
			return fmt.Errorf("codebuddy: activity chat status %d", http.StatusGatewayTimeout)
		},
	})

	p.runRound(context.Background(), activitySettings())

	mu.Lock()
	defer mu.Unlock()
	if len(pinged) != 1 {
		t.Fatalf("pinged %v, want exactly one auth before abort", pinged)
	}
	secondGot, _ := m.GetByID(second.ID)
	if secondGot.Metadata[cliproxyauth.MetadataKeyActivityPatrolAt] != nil {
		t.Fatalf("unprocessed auth was stamped: %#v", secondGot.Metadata)
	}
}

func TestActivityRunRound_AuthInvalidKeepsGoing(t *testing.T) {
	t.Parallel()

	first := globalCodeBuddyAuth("cb-a-" + uuid.NewString())
	second := globalCodeBuddyAuth("cb-b-" + uuid.NewString())
	m := newPatrolManager(t, first, second)

	var pinged int
	p := NewActivityPatrol(ActivityPatrolOptions{
		Store:    m,
		Settings: activitySettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		Ping: func(_ context.Context, _ *cliproxyauth.Auth, _ string) error {
			pinged++
			return fmt.Errorf("codebuddy: activity chat status 401")
		},
	})

	p.runRound(context.Background(), activitySettings())
	if pinged != 2 {
		t.Fatalf("pinged = %d, want 2", pinged)
	}
	got, _ := m.GetByID(first.ID)
	if got.Metadata[cliproxyauth.MetadataKeyActivityPatrolResult] != cliproxyauth.ActivityPatrolResultAuthInvalid {
		t.Fatalf("result = %#v", got.Metadata[cliproxyauth.MetadataKeyActivityPatrolResult])
	}
}

func TestActivityRunRound_WaitsBetweenAccounts(t *testing.T) {
	t.Parallel()

	a := globalCodeBuddyAuth("cb-a-" + uuid.NewString())
	b := globalCodeBuddyAuth("cb-b-" + uuid.NewString())
	m := newPatrolManager(t, a, b)

	var sleeps []time.Duration
	p := NewActivityPatrol(ActivityPatrolOptions{
		Store:    m,
		Settings: activitySettings,
		Intn:     func(int) int { return 0 },
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
		Ping: func(context.Context, *cliproxyauth.Auth, string) error { return nil },
	})

	p.runRound(context.Background(), activitySettings())
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Fatalf("sleeps = %v, want one min-account-interval", sleeps)
	}
}

func TestClassifyActivityError(t *testing.T) {
	t.Parallel()

	if got := classifyActivityError(fmt.Errorf("codebuddy: activity chat status 504")); got != "transient" {
		t.Fatalf("504 class = %q", got)
	}
	if got := classifyActivityError(&cliproxyauth.Error{Code: cliproxyauth.ErrorCodeCredentialCreditsExhausted, Message: "CodeBuddy credits exhausted"}); got != "credits_exhausted" {
		t.Fatalf("credits class = %q", got)
	}
	if got := classifyActivityError(fmt.Errorf("codebuddy: activity chat status 401")); got != "auth_invalid" {
		t.Fatalf("401 class = %q", got)
	}
}
