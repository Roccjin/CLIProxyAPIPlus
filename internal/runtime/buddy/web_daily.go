package buddy

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// WebDaily drives one completed international WorkBuddy web conversation per
// credential so the daily activity reward can settle. Success is recorded only
// after the conversation status is completed.
type WebDaily struct {
	store    AuthStore
	settings func() config.WorkBuddyWebDailyConfig
	run      func(ctx context.Context, auth *cliproxyauth.Auth, model string) error
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
	intn     func(n int) int
}

// WebDailyOptions configures the web daily loop.
type WebDailyOptions struct {
	Store    AuthStore
	Settings func() config.WorkBuddyWebDailyConfig
	Run      func(ctx context.Context, auth *cliproxyauth.Auth, model string) error
	Now      func() time.Time
	Sleep    func(ctx context.Context, d time.Duration) error
	Intn     func(n int) int
}

// NewWebDaily builds the daily web-conversation loop. Store is required.
func NewWebDaily(opts WebDailyOptions) *WebDaily {
	task := &WebDaily{
		store:    opts.Store,
		settings: opts.Settings,
		run:      opts.Run,
		now:      opts.Now,
		sleep:    opts.Sleep,
		intn:     opts.Intn,
	}
	if task.settings == nil {
		task.settings = config.DefaultWorkBuddyWebDailyConfig
	}
	if task.run == nil {
		task.run = func(ctx context.Context, auth *cliproxyauth.Auth, model string) error {
			return fmt.Errorf("workbuddy web daily: run not configured")
		}
	}
	if task.now == nil {
		task.now = time.Now
	}
	if task.sleep == nil {
		task.sleep = sleepContext
	}
	if task.intn == nil {
		task.intn = rand.Intn
	}
	return task
}

// RunWorkBuddyWebDaily is the production run used by the service wiring.
func RunWorkBuddyWebDaily(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, model string) error {
	_, err := executor.RunWorkBuddyWebDaily(ctx, auth, cfg, model)
	return err
}

// Run waits a startup jitter, then repeats serial rounds until ctx ends.
func (t *WebDaily) Run(ctx context.Context) {
	if t == nil || t.store == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	settings := t.currentSettings()
	if !settings.Enabled {
		return
	}
	if err := t.sleep(ctx, t.jitterDuration(settings.StartupJitter)); err != nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		settings = t.currentSettings()
		if !settings.Enabled {
			return
		}
		t.runRound(ctx, settings)
		if ctx.Err() != nil {
			return
		}
		settings = t.currentSettings()
		if !settings.Enabled {
			return
		}
		if err := t.sleep(ctx, settings.Interval); err != nil {
			return
		}
	}
}

func (t *WebDaily) runRound(ctx context.Context, settings config.WorkBuddyWebDailyConfig) {
	candidates := t.selectCandidates(t.now(), settings)
	t.shuffle(candidates)
	if len(candidates) == 0 {
		log.Debug("workbuddy web daily: no international credentials due")
		return
	}
	log.Infof("workbuddy web daily: running %d credential(s) model=%s", len(candidates), settings.Model)
	for i, auth := range candidates {
		if ctx.Err() != nil {
			return
		}
		if i > 0 {
			delay := settings.MinAccountInterval + t.jitterDuration(settings.AccountJitter)
			if err := t.sleep(ctx, delay); err != nil {
				return
			}
		}
		switch t.inspect(ctx, auth, settings) {
		case patrolResultCanceled:
			return
		case patrolResultAbortRound:
			log.Warnf("workbuddy web daily: stopping round after transient error on %s", auth.ID)
			return
		}
	}
}

func (t *WebDaily) selectCandidates(now time.Time, settings config.WorkBuddyWebDailyConfig) []*cliproxyauth.Auth {
	if t.store == nil {
		return nil
	}
	out := make([]*cliproxyauth.Auth, 0)
	for _, auth := range t.store.List() {
		if !isWebDailyCandidate(auth, now, settings.Interval) {
			continue
		}
		out = append(out, auth)
	}
	return out
}

func isWebDailyCandidate(auth *cliproxyauth.Auth, now time.Time, interval time.Duration) bool {
	if !allowsBuddyMaintenance(auth) {
		return false
	}
	if strings.ToLower(strings.TrimSpace(auth.Provider)) != "workbuddy" {
		return false
	}
	domain := metadataString(auth, "domain")
	if !workbuddy.IsGlobalDomain(domain) {
		return false
	}
	if strings.TrimSpace(metadataString(auth, "access_token")) == "" {
		return false
	}
	if strings.TrimSpace(metadataString(auth, "user_id")) == "" {
		return false
	}
	if interval > 0 {
		if at, ok := metadataTime(auth, cliproxyauth.MetadataKeyWorkBuddyWebDailyAt); ok && now.Sub(at) < interval {
			result := metadataString(auth, cliproxyauth.MetadataKeyWorkBuddyWebDailyResult)
			if result == cliproxyauth.WorkBuddyWebDailyResultOK || result == cliproxyauth.WorkBuddyWebDailyResultAuthInvalid {
				return false
			}
		}
	}
	return true
}

func (t *WebDaily) inspect(ctx context.Context, auth *cliproxyauth.Auth, settings config.WorkBuddyWebDailyConfig) patrolStep {
	if auth == nil {
		return patrolResultContinue
	}
	timeout := settings.RequestTimeout
	if timeout < 3*time.Minute {
		timeout = 3 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	err := t.run(runCtx, auth, settings.Model)
	cancel()
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return patrolResultCanceled
		}
		kind := classifyWebDailyError(err)
		log.Warnf("workbuddy web daily: run failed auth_id=%s class=%s: %v", auth.ID, kind, err)
		switch kind {
		case "transient":
			return patrolResultAbortRound
		case "auth_invalid":
			t.stamp(ctx, auth, cliproxyauth.WorkBuddyWebDailyResultAuthInvalid)
			return patrolResultContinue
		default:
			return patrolResultContinue
		}
	}
	t.stamp(ctx, auth, cliproxyauth.WorkBuddyWebDailyResultOK)
	log.Infof("workbuddy web daily: completed auth_id=%s", auth.ID)
	return patrolResultContinue
}

func (t *WebDaily) stamp(ctx context.Context, auth *cliproxyauth.Auth, result string) {
	if t.store == nil || auth == nil {
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Metadata[cliproxyauth.MetadataKeyWorkBuddyWebDailyAt] = t.now().UTC().Format(time.RFC3339Nano)
	auth.Metadata[cliproxyauth.MetadataKeyWorkBuddyWebDailyResult] = result
	if _, err := t.store.Update(ctx, auth); err != nil {
		log.Warnf("workbuddy web daily: persist metadata failed auth_id=%s: %v", auth.ID, err)
	}
}

func (t *WebDaily) currentSettings() config.WorkBuddyWebDailyConfig {
	settings := config.DefaultWorkBuddyWebDailyConfig()
	if t != nil && t.settings != nil {
		settings = t.settings()
	}
	settings.Normalize()
	return settings
}

func (t *WebDaily) shuffle(auths []*cliproxyauth.Auth) {
	n := len(auths)
	for i := n - 1; i > 0; i-- {
		j := t.intn(i + 1)
		auths[i], auths[j] = auths[j], auths[i]
	}
}

func (t *WebDaily) jitterDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	ms := int(max / time.Millisecond)
	if ms <= 0 {
		return 0
	}
	return time.Duration(t.intn(ms+1)) * time.Millisecond
}

func classifyWebDailyError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "status 401") || strings.Contains(msg, "status 403") {
		return "auth_invalid"
	}
	if strings.Contains(msg, "status 429") || strings.Contains(msg, "status 502") || strings.Contains(msg, "status 503") || strings.Contains(msg, "status 504") {
		return "transient"
	}
	return "failed"
}
