package cliproxy

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/buddy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

func (s *Service) syncBuddyCreditsPatrol() {
	if s == nil {
		return
	}

	s.creditsPatrolMu.Lock()
	defer s.creditsPatrolMu.Unlock()

	if s.creditsPatrolCancel != nil {
		s.creditsPatrolCancel()
		s.creditsPatrolCancel = nil
	}

	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	if s.coreManager == nil || cfg == nil || cfg.Home.Enabled || !cfg.BuddyCreditsPatrol.Enabled {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.creditsPatrolCancel = cancel
	patrol := buddy.NewCreditsPatrol(buddy.CreditsPatrolOptions{
		Store:    s.coreManager,
		Settings: s.buddyCreditsPatrolSettings,
		FetchQuota: func(fetchCtx context.Context, auth *coreauth.Auth) (float64, error) {
			s.cfgMu.RLock()
			current := s.cfg
			s.cfgMu.RUnlock()
			return buddy.FetchQuota(fetchCtx, auth, current)
		},
	})
	go patrol.Run(ctx)
	log.Infof("buddy credits patrol started (interval=%s, min-account-interval=%s)", cfg.BuddyCreditsPatrol.Interval, cfg.BuddyCreditsPatrol.MinAccountInterval)
}

func (s *Service) stopBuddyCreditsPatrol() {
	if s == nil {
		return
	}
	s.creditsPatrolMu.Lock()
	cancel := s.creditsPatrolCancel
	s.creditsPatrolCancel = nil
	s.creditsPatrolMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) buddyCreditsPatrolSettings() config.BuddyCreditsPatrolConfig {
	if s == nil {
		return config.DefaultBuddyCreditsPatrolConfig()
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfg == nil {
		return config.DefaultBuddyCreditsPatrolConfig()
	}
	settings := s.cfg.BuddyCreditsPatrol
	settings.Normalize()
	return settings
}
