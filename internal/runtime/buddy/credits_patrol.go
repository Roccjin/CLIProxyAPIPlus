package buddy

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	patrolResultContinue patrolStep = iota
	patrolResultAbortRound
	patrolResultCanceled
)

type patrolStep int

// AuthStore is the Auth Manager surface used by credits patrol.
type AuthStore interface {
	List() []*cliproxyauth.Auth
	Update(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error)
}

// CreditsPatrol serially inspects credits-exhausted Buddy credentials and
// re-enables them when billing reports remaining credits.
type CreditsPatrol struct {
	store      AuthStore
	settings   func() config.BuddyCreditsPatrolConfig
	fetchQuota func(ctx context.Context, auth *cliproxyauth.Auth) (float64, error)
	now        func() time.Time
	sleep      func(ctx context.Context, d time.Duration) error
	intn       func(n int) int
}

// CreditsPatrolOptions configures a patrol loop.
type CreditsPatrolOptions struct {
	Store      AuthStore
	Settings   func() config.BuddyCreditsPatrolConfig
	FetchQuota func(ctx context.Context, auth *cliproxyauth.Auth) (float64, error)
	Now        func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) error
	Intn       func(n int) int
}

// NewCreditsPatrol builds a patrol loop. Store and FetchQuota are required
// for production; tests may inject Now/Sleep/Intn.
func NewCreditsPatrol(opts CreditsPatrolOptions) *CreditsPatrol {
	p := &CreditsPatrol{
		store:      opts.Store,
		settings:   opts.Settings,
		fetchQuota: opts.FetchQuota,
		now:        opts.Now,
		sleep:      opts.Sleep,
		intn:       opts.Intn,
	}
	if p.settings == nil {
		p.settings = config.DefaultBuddyCreditsPatrolConfig
	}
	if p.fetchQuota == nil {
		p.fetchQuota = func(context.Context, *cliproxyauth.Auth) (float64, error) {
			return 0, fmt.Errorf("buddy credits patrol: quota fetcher not configured")
		}
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.sleep == nil {
		p.sleep = sleepContext
	}
	if p.intn == nil {
		p.intn = rand.Intn
	}
	return p
}

// FetchQuota loads remaining credits for a Buddy auth using the existing
// billing-meter clients. It never sends a chat completion.
func FetchQuota(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) (float64, error) {
	if auth == nil {
		return 0, fmt.Errorf("buddy credits patrol: auth is nil")
	}
	switch strings.ToLower(strings.TrimSpace(auth.Provider)) {
	case "workbuddy":
		quota, err := executor.FetchWorkBuddyQuota(ctx, auth, cfg)
		if err != nil {
			return 0, err
		}
		if quota == nil {
			return 0, nil
		}
		return quota.TotalRemain, nil
	case "codebuddy":
		quota, err := executor.FetchCodeBuddyQuota(ctx, auth, cfg)
		if err != nil {
			return 0, err
		}
		if quota == nil {
			return 0, nil
		}
		return float64(quota.TotalRemain), nil
	default:
		return 0, fmt.Errorf("buddy credits patrol: unsupported provider %q", auth.Provider)
	}
}

// Run waits a startup jitter, then repeats serial patrol rounds until ctx ends.
func (p *CreditsPatrol) Run(ctx context.Context) {
	if p == nil || p.store == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	settings := p.currentSettings()
	if !settings.Enabled {
		return
	}
	if err := p.sleep(ctx, p.jitterDuration(settings.StartupJitter)); err != nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		settings = p.currentSettings()
		if !settings.Enabled {
			return
		}
		p.runRound(ctx, settings)
		if ctx.Err() != nil {
			return
		}
		settings = p.currentSettings()
		if !settings.Enabled {
			return
		}
		if err := p.sleep(ctx, settings.Interval); err != nil {
			return
		}
	}
}

func (p *CreditsPatrol) runRound(ctx context.Context, settings config.BuddyCreditsPatrolConfig) {
	candidates := p.selectCandidates(p.now(), settings)
	p.shuffle(candidates)
	if len(candidates) == 0 {
		log.Debug("buddy credits patrol: no credits-exhausted Buddy credentials")
		return
	}
	log.Infof("buddy credits patrol: inspecting %d credential(s)", len(candidates))
	for i, auth := range candidates {
		if ctx.Err() != nil {
			return
		}
		if i > 0 {
			delay := settings.MinAccountInterval + p.jitterDuration(settings.AccountJitter)
			if err := p.sleep(ctx, delay); err != nil {
				return
			}
		}
		switch p.inspect(ctx, auth, settings) {
		case patrolResultCanceled:
			return
		case patrolResultAbortRound:
			log.Warnf("buddy credits patrol: stopping round after transient error on %s", auth.ID)
			return
		}
	}
}

func (p *CreditsPatrol) selectCandidates(now time.Time, settings config.BuddyCreditsPatrolConfig) []*cliproxyauth.Auth {
	if p.store == nil {
		return nil
	}
	out := make([]*cliproxyauth.Auth, 0)
	for _, auth := range p.store.List() {
		if !isCreditsPatrolCandidate(auth, now, settings.Interval) {
			continue
		}
		out = append(out, auth)
	}
	return out
}

func isCreditsPatrolCandidate(auth *cliproxyauth.Auth, now time.Time, interval time.Duration) bool {
	if auth == nil || !auth.IsDisabled() || cliproxyauth.IsPluginVirtualAuth(auth) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(auth.Provider)) {
	case "workbuddy", "codebuddy":
	default:
		return false
	}
	if metadataString(auth, cliproxyauth.MetadataKeyDisabledReason) != cliproxyauth.DisabledReasonCreditsExhausted {
		return false
	}
	if interval > 0 {
		if at, ok := metadataTime(auth, cliproxyauth.MetadataKeyCreditsPatrolAt); ok && now.Sub(at) < interval {
			return false
		}
	}
	return true
}

func (p *CreditsPatrol) inspect(ctx context.Context, auth *cliproxyauth.Auth, settings config.BuddyCreditsPatrolConfig) patrolStep {
	if auth == nil {
		return patrolResultContinue
	}
	accessBefore, refreshBefore := authTokens(auth)
	timeout := settings.RequestTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	remain, err := p.fetchQuota(fetchCtx, auth)
	cancel()
	p.persistIfTokensChanged(ctx, auth, accessBefore, refreshBefore)

	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return patrolResultCanceled
		}
		kind := classifyQuotaError(err)
		log.Warnf("buddy credits patrol: quota fetch failed auth_id=%s provider=%s class=%s: %v", auth.ID, auth.Provider, kind, err)
		switch kind {
		case "transient":
			return patrolResultAbortRound
		case "auth_invalid":
			p.stampPatrol(ctx, auth, cliproxyauth.CreditsPatrolResultAuthInvalid, 0)
			return patrolResultContinue
		default:
			return patrolResultContinue
		}
	}

	if remain >= settings.MinRemain {
		if errEnable := p.reenable(ctx, auth); errEnable != nil {
			log.Warnf("buddy credits patrol: re-enable failed auth_id=%s: %v", auth.ID, errEnable)
			return patrolResultContinue
		}
		p.stampPatrol(ctx, auth, cliproxyauth.CreditsPatrolResultReenabled, remain)
		log.Infof("buddy credits patrol: re-enabled auth_id=%s provider=%s remain=%.2f", auth.ID, auth.Provider, remain)
		return patrolResultContinue
	}

	p.stampPatrol(ctx, auth, cliproxyauth.CreditsPatrolResultStillEmpty, remain)
	log.Infof("buddy credits patrol: credits still empty auth_id=%s remain=%.2f", auth.ID, remain)
	return patrolResultContinue
}

func (p *CreditsPatrol) reenable(ctx context.Context, auth *cliproxyauth.Auth) error {
	if p.store == nil || auth == nil {
		return fmt.Errorf("buddy credits patrol: missing store or auth")
	}
	auth.Disabled = false
	auth.Status = cliproxyauth.StatusActive
	auth.StatusMessage = ""
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Metadata["disabled"] = false
	delete(auth.Metadata, cliproxyauth.MetadataKeyDisabledReason)
	delete(auth.Metadata, cliproxyauth.MetadataKeyDisabledProviderCode)
	delete(auth.Metadata, cliproxyauth.MetadataKeyDisabledAt)
	_, err := p.store.Update(ctx, auth)
	return err
}

func (p *CreditsPatrol) stampPatrol(ctx context.Context, auth *cliproxyauth.Auth, result string, remain float64) {
	if p.store == nil || auth == nil {
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Metadata[cliproxyauth.MetadataKeyCreditsPatrolAt] = p.now().UTC().Format(time.RFC3339Nano)
	auth.Metadata[cliproxyauth.MetadataKeyCreditsPatrolResult] = result
	auth.Metadata[cliproxyauth.MetadataKeyCreditsPatrolRemain] = remain
	if _, err := p.store.Update(ctx, auth); err != nil {
		log.Warnf("buddy credits patrol: persist patrol metadata failed auth_id=%s: %v", auth.ID, err)
	}
}

func (p *CreditsPatrol) persistIfTokensChanged(ctx context.Context, auth *cliproxyauth.Auth, accessBefore, refreshBefore string) {
	if p.store == nil || auth == nil {
		return
	}
	accessAfter, refreshAfter := authTokens(auth)
	if accessAfter == accessBefore && refreshAfter == refreshBefore {
		return
	}
	if _, err := p.store.Update(ctx, auth); err != nil {
		log.Warnf("buddy credits patrol: persist refreshed tokens failed auth_id=%s: %v", auth.ID, err)
	}
}

func (p *CreditsPatrol) currentSettings() config.BuddyCreditsPatrolConfig {
	settings := config.DefaultBuddyCreditsPatrolConfig()
	if p != nil && p.settings != nil {
		settings = p.settings()
	}
	settings.Normalize()
	return settings
}

func (p *CreditsPatrol) shuffle(auths []*cliproxyauth.Auth) {
	n := len(auths)
	for i := n - 1; i > 0; i-- {
		j := p.intn(i + 1)
		auths[i], auths[j] = auths[j], auths[i]
	}
}

func (p *CreditsPatrol) jitterDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	ms := int(max / time.Millisecond)
	if ms <= 0 {
		return 0
	}
	return time.Duration(p.intn(ms+1)) * time.Millisecond
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func classifyQuotaError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "transient"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "status 401") ||
		strings.Contains(msg, "status 403") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "unauthenticated") {
		return "auth_invalid"
	}
	for _, code := range []string{"status 408", "status 429", "status 500", "status 502", "status 503", "status 504"} {
		if strings.Contains(msg, code) {
			return "transient"
		}
	}
	if strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "temporary failure") {
		return "transient"
	}
	return "other"
}

func metadataString(auth *cliproxyauth.Auth, key string) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	raw, ok := auth.Metadata[key]
	if !ok || raw == nil {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func metadataTime(auth *cliproxyauth.Auth, key string) (time.Time, bool) {
	raw := metadataString(auth, key)
	if raw == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func authTokens(auth *cliproxyauth.Auth) (accessToken, refreshToken string) {
	if auth == nil || auth.Metadata == nil {
		return "", ""
	}
	if v, ok := auth.Metadata["access_token"].(string); ok {
		accessToken = strings.TrimSpace(v)
	}
	if v, ok := auth.Metadata["refresh_token"].(string); ok {
		refreshToken = strings.TrimSpace(v)
	}
	return accessToken, refreshToken
}
