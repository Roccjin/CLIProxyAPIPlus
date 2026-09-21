package cliproxy

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/buddy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

func (s *Service) syncBuddyActivityPatrol() {
	if s == nil {
		return
	}

	s.activityPatrolMu.Lock()
	defer s.activityPatrolMu.Unlock()

	if s.activityPatrolCancel != nil {
		s.activityPatrolCancel()
		s.activityPatrolCancel = nil
	}

	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	if s.coreManager == nil || cfg == nil || cfg.Home.Enabled || !cfg.BuddyActivityPatrol.Enabled {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.activityPatrolCancel = cancel
	patrol := buddy.NewActivityPatrol(buddy.ActivityPatrolOptions{
		Store:    s.coreManager,
		Settings: s.buddyActivityPatrolSettings,
		Ping: func(pingCtx context.Context, auth *coreauth.Auth, model string) error {
			s.cfgMu.RLock()
			current := s.cfg
			s.cfgMu.RUnlock()
			return buddy.PingCodeBuddyActivity(pingCtx, auth, current, model)
		},
	})
	go patrol.Run(ctx)
	log.Infof("buddy activity patrol started (interval=%s, min-account-interval=%s, model=%s)", cfg.BuddyActivityPatrol.Interval, cfg.BuddyActivityPatrol.MinAccountInterval, cfg.BuddyActivityPatrol.Model)
}

func (s *Service) stopBuddyActivityPatrol() {
	if s == nil {
		return
	}
	s.activityPatrolMu.Lock()
	cancel := s.activityPatrolCancel
	s.activityPatrolCancel = nil
	s.activityPatrolMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) buddyActivityPatrolSettings() config.BuddyActivityPatrolConfig {
	if s == nil {
		return config.DefaultBuddyActivityPatrolConfig()
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfg == nil {
		return config.DefaultBuddyActivityPatrolConfig()
	}
	settings := s.cfg.BuddyActivityPatrol
	settings.Normalize()
	return settings
}
