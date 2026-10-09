package buddy

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// ReserveStore is the auth surface used after a successful Buddy call.
type ReserveStore interface {
	GetByID(id string) (*cliproxyauth.Auth, bool)
	Update(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error)
	ApplyBuddyCreditsFloor(ctx context.Context, authID string, remain, floor float64) bool
}

// ReserveGuard samples Buddy billing after successful calls when min-remain is
// above 1. Samples for one auth are spaced by min-account-interval.
type ReserveGuard struct {
	store      ReserveStore
	settings   func() config.BuddyCreditsPatrolConfig
	fetchQuota func(ctx context.Context, auth *cliproxyauth.Auth) (float64, error)
	now        func() time.Time
	async      bool

	mu       sync.Mutex
	last     map[string]time.Time
	inflight map[string]struct{}
}

// ReserveGuardOptions configures post-call credit floor checks.
type ReserveGuardOptions struct {
	Store      ReserveStore
	Settings   func() config.BuddyCreditsPatrolConfig
	FetchQuota func(ctx context.Context, auth *cliproxyauth.Auth) (float64, error)
	Now        func() time.Time
}

// NewReserveGuard builds a guard. Store and FetchQuota are required in production.
func NewReserveGuard(opts ReserveGuardOptions) *ReserveGuard {
	g := &ReserveGuard{
		store:      opts.Store,
		settings:   opts.Settings,
		fetchQuota: opts.FetchQuota,
		now:        opts.Now,
		async:      true,
		last:       map[string]time.Time{},
		inflight:   map[string]struct{}{},
	}
	if g.settings == nil {
		g.settings = config.DefaultBuddyCreditsPatrolConfig
	}
	if g.now == nil {
		g.now = time.Now
	}
	return g
}

// Observe schedules a throttled billing sample. It returns without waiting.
func (g *ReserveGuard) Observe(authID, provider string) {
	if g == nil || g.store == nil || g.fetchQuota == nil {
		return
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "workbuddy" && provider != "codebuddy" {
		return
	}
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return
	}
	settings := g.currentSettings()
	if !settings.Enabled || settings.MinRemain <= 1 {
		return
	}
	if !g.claim(authID, settings.MinAccountInterval) {
		return
	}
	if g.async {
		go g.check(authID, settings)
		return
	}
	g.check(authID, settings)
}

func (g *ReserveGuard) check(authID string, settings config.BuddyCreditsPatrolConfig) {
	defer g.finish(authID)
	auth, ok := g.store.GetByID(authID)
	if !ok || auth == nil || auth.IsDisabled() || cliproxyauth.IsPluginVirtualAuth(auth) {
		return
	}
	switch strings.ToLower(strings.TrimSpace(auth.Provider)) {
	case "workbuddy", "codebuddy":
	default:
		return
	}
	timeout := settings.RequestTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	accessBefore, refreshBefore := authTokens(auth)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	remain, err := g.fetchQuota(ctx, auth)
	cancel()
	if accessAfter, refreshAfter := authTokens(auth); accessAfter != accessBefore || refreshAfter != refreshBefore {
		if _, updateErr := g.store.Update(context.Background(), auth); updateErr != nil {
			log.Warnf("buddy credits reserve: persist refreshed tokens failed auth_id=%s: %v", authID, updateErr)
		}
	}
	if err != nil {
		log.Warnf("buddy credits reserve: quota fetch failed auth_id=%s: %v", authID, err)
		return
	}
	if remain >= settings.MinRemain {
		return
	}
	if g.store.ApplyBuddyCreditsFloor(context.Background(), authID, remain, settings.MinRemain) {
		log.Infof("buddy credits reserve: parked auth_id=%s provider=%s remain=%.2f min=%.2f", authID, auth.Provider, remain, settings.MinRemain)
	}
}

func (g *ReserveGuard) claim(authID string, minGap time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, busy := g.inflight[authID]; busy {
		return false
	}
	if last, ok := g.last[authID]; ok && minGap > 0 && g.now().Sub(last) < minGap {
		return false
	}
	g.inflight[authID] = struct{}{}
	g.last[authID] = g.now()
	return true
}

func (g *ReserveGuard) finish(authID string) {
	g.mu.Lock()
	delete(g.inflight, authID)
	g.mu.Unlock()
}

func (g *ReserveGuard) currentSettings() config.BuddyCreditsPatrolConfig {
	settings := config.DefaultBuddyCreditsPatrolConfig()
	if g != nil && g.settings != nil {
		settings = g.settings()
	}
	settings.Normalize()
	return settings
}

// allowsBuddyMaintenance reports whether a background Buddy task may use the auth.
// Accounts parked only for the credit floor stay eligible. Exhausted, manual,
// and other disables do not.
func allowsBuddyMaintenance(auth *cliproxyauth.Auth) bool {
	if auth == nil || cliproxyauth.IsPluginVirtualAuth(auth) {
		return false
	}
	if !auth.IsDisabled() {
		return true
	}
	return metadataString(auth, cliproxyauth.MetadataKeyDisabledReason) == cliproxyauth.DisabledReasonCreditsReserve
}
