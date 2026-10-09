package buddy

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func webDailySettings() config.WorkBuddyWebDailyConfig {
	cfg := config.DefaultWorkBuddyWebDailyConfig()
	cfg.StartupJitter = 0
	cfg.AccountJitter = 0
	cfg.MinAccountInterval = time.Millisecond
	cfg.Interval = 24 * time.Hour
	cfg.RequestTimeout = 3 * time.Minute
	return cfg
}

func globalWorkBuddyAuth(id string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID:       id,
		Provider: "workbuddy",
		Status:   cliproxyauth.StatusActive,
		Metadata: map[string]any{
			"type":         "workbuddy",
			"access_token": "token",
			"user_id":      "user-1",
			"domain":       "www.workbuddy.ai",
		},
	}
}

func TestSelectWebDailyCandidates_OnlyGlobalWorkBuddy(t *testing.T) {
	t.Parallel()

	ok := globalWorkBuddyAuth("wb-" + uuid.NewString())
	cn := globalWorkBuddyAuth("cn-" + uuid.NewString())
	cn.Metadata["domain"] = "www.workbuddy.cn"
	cb := globalWorkBuddyAuth("cb-" + uuid.NewString())
	cb.Provider = "codebuddy"
	cb.Metadata["domain"] = "www.codebuddy.ai"
	recent := globalWorkBuddyAuth("recent-" + uuid.NewString())
	recent.Metadata[cliproxyauth.MetadataKeyWorkBuddyWebDailyAt] = time.Now().UTC().Format(time.RFC3339Nano)
	recent.Metadata[cliproxyauth.MetadataKeyWorkBuddyWebDailyResult] = cliproxyauth.WorkBuddyWebDailyResultOK
	failed := globalWorkBuddyAuth("failed-" + uuid.NewString())
	failed.Metadata[cliproxyauth.MetadataKeyWorkBuddyWebDailyAt] = time.Now().UTC().Format(time.RFC3339Nano)
	failed.Metadata[cliproxyauth.MetadataKeyWorkBuddyWebDailyResult] = "failed"

	m := newPatrolManager(t, ok, cn, cb, recent, failed)
	task := NewWebDaily(WebDailyOptions{
		Store:    m,
		Settings: webDailySettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
	})
	got := task.selectCandidates(time.Now(), webDailySettings())
	ids := map[string]bool{}
	for _, auth := range got {
		ids[auth.ID] = true
	}
	if !ids[ok.ID] || !ids[failed.ID] || ids[cn.ID] || ids[cb.ID] || ids[recent.ID] {
		t.Fatalf("candidates = %#v", ids)
	}
}

func TestSelectWebDailyCandidates_ReserveDisableStillRuns(t *testing.T) {
	t.Parallel()
	reserve := globalWorkBuddyAuth("reserve-" + uuid.NewString())
	reserve.Disabled = true
	reserve.Status = cliproxyauth.StatusDisabled
	reserve.Metadata["disabled"] = true
	reserve.Metadata[cliproxyauth.MetadataKeyDisabledReason] = cliproxyauth.DisabledReasonCreditsReserve
	exhausted := globalWorkBuddyAuth("empty-" + uuid.NewString())
	exhausted.Disabled = true
	exhausted.Status = cliproxyauth.StatusDisabled
	exhausted.Metadata["disabled"] = true
	exhausted.Metadata[cliproxyauth.MetadataKeyDisabledReason] = cliproxyauth.DisabledReasonCreditsExhausted
	if !isWebDailyCandidate(reserve, time.Now(), time.Hour) {
		t.Fatal("credits-reserve auth was skipped")
	}
	if isWebDailyCandidate(exhausted, time.Now(), time.Hour) {
		t.Fatal("credits-exhausted auth was selected")
	}
}

func TestWebDailyRound_StampsOnlyCompleted(t *testing.T) {
	ok := globalWorkBuddyAuth("ok-" + uuid.NewString())
	pending := globalWorkBuddyAuth("pending-" + uuid.NewString())
	m := newPatrolManager(t, ok, pending)
	task := NewWebDaily(WebDailyOptions{
		Store:    m,
		Settings: webDailySettings,
		Intn:     func(int) int { return 0 },
		Sleep:    func(context.Context, time.Duration) error { return nil },
		Run: func(_ context.Context, auth *cliproxyauth.Auth, model string) error {
			if model != config.DefaultWorkBuddyWebDailyModel {
				return fmt.Errorf("model %s", model)
			}
			if auth.ID == pending.ID {
				return fmt.Errorf("workbuddy web daily: conversation not completed (working)")
			}
			return nil
		},
	})
	task.runRound(context.Background(), webDailySettings())

	got, okFound := m.GetByID(ok.ID)
	if !okFound {
		t.Fatal("missing completed auth")
	}
	if got.Metadata[cliproxyauth.MetadataKeyWorkBuddyWebDailyResult] != cliproxyauth.WorkBuddyWebDailyResultOK {
		t.Fatalf("completed result = %#v", got.Metadata[cliproxyauth.MetadataKeyWorkBuddyWebDailyResult])
	}
	pendingGot, pendingFound := m.GetByID(pending.ID)
	if !pendingFound {
		t.Fatal("missing pending auth")
	}
	if _, exists := pendingGot.Metadata[cliproxyauth.MetadataKeyWorkBuddyWebDailyAt]; exists {
		t.Fatal("incomplete conversation was stamped")
	}
}
