package cliproxy

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/buddy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

func (s *Service) syncWorkBuddyWebDaily() {
	if s == nil {
		return
	}

	s.webDailyMu.Lock()
	defer s.webDailyMu.Unlock()

	if s.webDailyCancel != nil {
		s.webDailyCancel()
		s.webDailyCancel = nil
	}

	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	if s.coreManager == nil || cfg == nil || cfg.Home.Enabled || !cfg.WorkBuddyWebDaily.Enabled {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.webDailyCancel = cancel
	task := buddy.NewWebDaily(buddy.WebDailyOptions{
		Store:    s.coreManager,
		Settings: s.workBuddyWebDailySettings,
		Run: func(runCtx context.Context, auth *coreauth.Auth, model string) error {
			s.cfgMu.RLock()
			current := s.cfg
			s.cfgMu.RUnlock()
			return buddy.RunWorkBuddyWebDaily(runCtx, auth, current, model)
		},
	})
	go task.Run(ctx)
	log.Infof("workbuddy web daily started (interval=%s, min-account-interval=%s, model=%s)", cfg.WorkBuddyWebDaily.Interval, cfg.WorkBuddyWebDaily.MinAccountInterval, cfg.WorkBuddyWebDaily.Model)
}

func (s *Service) stopWorkBuddyWebDaily() {
	if s == nil {
		return
	}
	s.webDailyMu.Lock()
	cancel := s.webDailyCancel
	s.webDailyCancel = nil
	s.webDailyMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) workBuddyWebDailySettings() config.WorkBuddyWebDailyConfig {
	if s == nil {
		return config.DefaultWorkBuddyWebDailyConfig()
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfg == nil {
		return config.DefaultWorkBuddyWebDailyConfig()
	}
	settings := s.cfg.WorkBuddyWebDaily
	settings.Normalize()
	return settings
}
