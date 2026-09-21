package buddy

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// ActivityPatrol serially pings international CodeBuddy credentials with one
// official-CLI-shaped chat plus /v2/report events so the daily gift pack can settle.
type ActivityPatrol struct {
	store    AuthStore
	settings func() config.BuddyActivityPatrolConfig
	ping     func(ctx context.Context, auth *cliproxyauth.Auth, model string) error
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
	intn     func(n int) int
}

// ActivityPatrolOptions configures the activity loop.
type ActivityPatrolOptions struct {
	Store    AuthStore
	Settings func() config.BuddyActivityPatrolConfig
	Ping     func(ctx context.Context, auth *cliproxyauth.Auth, model string) error
	Now      func() time.Time
	Sleep    func(ctx context.Context, d time.Duration) error
	Intn     func(n int) int
}

// NewActivityPatrol builds an activity patrol loop. Store is required.
func NewActivityPatrol(opts ActivityPatrolOptions) *ActivityPatrol {
	p := &ActivityPatrol{
		store:    opts.Store,
		settings: opts.Settings,
		ping:     opts.Ping,
		now:      opts.Now,
		sleep:    opts.Sleep,
		intn:     opts.Intn,
	}
	if p.settings == nil {
		p.settings = config.DefaultBuddyActivityPatrolConfig
	}
	if p.ping == nil {
		p.ping = func(ctx context.Context, auth *cliproxyauth.Auth, model string) error {
			return fmt.Errorf("buddy activity patrol: ping not configured")
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

// PingCodeBuddyActivity is the production ping used by the service wiring.
func PingCodeBuddyActivity(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, model string) error {
	_, err := executor.PingCodeBuddyDailyActivity(ctx, auth, cfg, model)
	return err
}

// Run waits a startup jitter, then repeats serial patrol rounds until ctx ends.
func (p *ActivityPatrol) Run(ctx context.Context) {
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

func (p *ActivityPatrol) runRound(ctx context.Context, settings config.BuddyActivityPatrolConfig) {
	candidates := p.selectCandidates(p.now(), settings)
	p.shuffle(candidates)
	if len(candidates) == 0 {
		log.Debug("buddy activity patrol: no international CodeBuddy credentials due")
		return
	}
	log.Infof("buddy activity patrol: pinging %d credential(s) model=%s", len(candidates), settings.Model)
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
			log.Warnf("buddy activity patrol: stopping round after transient error on %s", auth.ID)
			return
		}
	}
}

func (p *ActivityPatrol) selectCandidates(now time.Time, settings config.BuddyActivityPatrolConfig) []*cliproxyauth.Auth {
	if p.store == nil {
		return nil
	}
	out := make([]*cliproxyauth.Auth, 0)
	for _, auth := range p.store.List() {
		if !isActivityPatrolCandidate(auth, now, settings.Interval) {
			continue
		}
		out = append(out, auth)
	}
	return out
}

func isActivityPatrolCandidate(auth *cliproxyauth.Auth, now time.Time, interval time.Duration) bool {
	if auth == nil || auth.IsDisabled() || cliproxyauth.IsPluginVirtualAuth(auth) {
		return false
	}
	if strings.ToLower(strings.TrimSpace(auth.Provider)) != "codebuddy" {
		return false
	}
	domain := metadataString(auth, "domain")
	if !codebuddy.IsGlobalDomain(domain) {
		return false
	}
	if strings.TrimSpace(metadataString(auth, "access_token")) == "" {
		return false
	}
	if interval > 0 {
		if at, ok := metadataTime(auth, cliproxyauth.MetadataKeyActivityPatrolAt); ok && now.Sub(at) < interval {
			return false
		}
	}
	return true
}

func (p *ActivityPatrol) inspect(ctx context.Context, auth *cliproxyauth.Auth, settings config.BuddyActivityPatrolConfig) patrolStep {
	if auth == nil {
		return patrolResultContinue
	}
	timeout := settings.RequestTimeout
	if timeout < 3*time.Minute {
		timeout = 3 * time.Minute
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	err := p.ping(pingCtx, auth, settings.Model)
	cancel()

	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			p.persistMachineID(ctx, auth)
			return patrolResultCanceled
		}
		kind := classifyActivityError(err)
		log.Warnf("buddy activity patrol: ping failed auth_id=%s class=%s: %v", auth.ID, kind, err)
		switch kind {
		case "transient":
			p.persistMachineID(ctx, auth)
			return patrolResultAbortRound
		case "auth_invalid":
			p.stampPatrol(ctx, auth, cliproxyauth.ActivityPatrolResultAuthInvalid)
			return patrolResultContinue
		case "credits_exhausted":
			p.stampPatrol(ctx, auth, cliproxyauth.ActivityPatrolResultCreditsEmpty)
			return patrolResultContinue
		default:
			p.stampPatrol(ctx, auth, cliproxyauth.ActivityPatrolResultFailed)
			return patrolResultContinue
		}
	}

	p.stampPatrol(ctx, auth, cliproxyauth.ActivityPatrolResultOK)
	log.Infof("buddy activity patrol: pinged auth_id=%s", auth.ID)
	return patrolResultContinue
}

func (p *ActivityPatrol) stampPatrol(ctx context.Context, auth *cliproxyauth.Auth, result string) {
	if p.store == nil || auth == nil {
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Metadata[cliproxyauth.MetadataKeyActivityPatrolAt] = p.now().UTC().Format(time.RFC3339Nano)
	auth.Metadata[cliproxyauth.MetadataKeyActivityPatrolResult] = result
	if _, err := p.store.Update(ctx, auth); err != nil {
		log.Warnf("buddy activity patrol: persist patrol metadata failed auth_id=%s: %v", auth.ID, err)
	}
}

func (p *ActivityPatrol) persistMachineID(ctx context.Context, auth *cliproxyauth.Auth) {
	if p.store == nil || auth == nil {
		return
	}
	if metadataString(auth, cliproxyauth.MetadataKeyCLIMachineID) == "" {
		return
	}
	if _, err := p.store.Update(ctx, auth); err != nil {
		log.Warnf("buddy activity patrol: persist machine id failed auth_id=%s: %v", auth.ID, err)
	}
}

func (p *ActivityPatrol) currentSettings() config.BuddyActivityPatrolConfig {
	settings := config.DefaultBuddyActivityPatrolConfig()
	if p != nil && p.settings != nil {
		settings = p.settings()
	}
	settings.Normalize()
	return settings
}

func (p *ActivityPatrol) shuffle(auths []*cliproxyauth.Auth) {
	n := len(auths)
	for i := n - 1; i > 0; i-- {
		j := p.intn(i + 1)
		auths[i], auths[j] = auths[j], auths[i]
	}
}

func (p *ActivityPatrol) jitterDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	ms := int(max / time.Millisecond)
	if ms <= 0 {
		return 0
	}
	return time.Duration(p.intn(ms+1)) * time.Millisecond
}

func classifyActivityError(err error) string {
	if err == nil {
		return ""
	}
	var authErr *cliproxyauth.Error
	if errors.As(err, &authErr) && authErr != nil {
		switch authErr.Code {
		case cliproxyauth.ErrorCodeCredentialCreditsExhausted:
			return "credits_exhausted"
		case cliproxyauth.ErrorCodeUpstreamGatewayTimeout:
			return "transient"
		}
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "14018") || strings.Contains(msg, "credits exhausted") {
		return "credits_exhausted"
	}
	return classifyQuotaError(err)
}
