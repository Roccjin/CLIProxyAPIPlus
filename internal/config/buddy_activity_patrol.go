package config

import (
	"strings"
	"time"
)

const (
	defaultBuddyActivityPatrolInterval           = 24 * time.Hour
	defaultBuddyActivityPatrolStartupJitter      = 10 * time.Minute
	defaultBuddyActivityPatrolMinAccountInterval = 45 * time.Second
	defaultBuddyActivityPatrolAccountJitter      = 30 * time.Second
	defaultBuddyActivityPatrolRequestTimeout     = 90 * time.Second
	// DefaultBuddyActivityPatrolModel is the cheap CodeBuddy international
	// model used for the daily CLI activity ping.
	DefaultBuddyActivityPatrolModel = "hy3"
)

// BuddyActivityPatrolConfig controls the background loop that sends one
// official-CLI-shaped chat plus /v2/report events per international
// CodeBuddy credential, so the next-day gift pack can settle.
type BuddyActivityPatrolConfig struct {
	// Enabled starts the patrol loop. Default true.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Interval is the minimum time between full patrol rounds. Default 24h.
	Interval time.Duration `yaml:"interval" json:"interval"`
	// StartupJitter is the maximum random delay before the first round. Default 10m.
	StartupJitter time.Duration `yaml:"startup-jitter" json:"startup-jitter"`
	// MinAccountInterval is the minimum pause between two account pings. Default 45s.
	MinAccountInterval time.Duration `yaml:"min-account-interval" json:"min-account-interval"`
	// AccountJitter is extra random delay added after MinAccountInterval. Default 30s.
	AccountJitter time.Duration `yaml:"account-jitter" json:"account-jitter"`
	// RequestTimeout bounds one account's report+chat+report sequence. Default 90s.
	RequestTimeout time.Duration `yaml:"request-timeout" json:"request-timeout"`
	// Model is the chat model used for the ping. Default hy3.
	Model string `yaml:"model" json:"model"`
}

// DefaultBuddyActivityPatrolConfig returns the default-on daily serial patrol.
func DefaultBuddyActivityPatrolConfig() BuddyActivityPatrolConfig {
	return BuddyActivityPatrolConfig{
		Enabled:            true,
		Interval:           defaultBuddyActivityPatrolInterval,
		StartupJitter:      defaultBuddyActivityPatrolStartupJitter,
		MinAccountInterval: defaultBuddyActivityPatrolMinAccountInterval,
		AccountJitter:      defaultBuddyActivityPatrolAccountJitter,
		RequestTimeout:     defaultBuddyActivityPatrolRequestTimeout,
		Model:              DefaultBuddyActivityPatrolModel,
	}
}

// Normalize fills invalid duration/model values with defaults.
// Enabled is left unchanged so an explicit false remains off.
func (c *BuddyActivityPatrolConfig) Normalize() {
	if c == nil {
		return
	}
	if c.Interval <= 0 {
		c.Interval = defaultBuddyActivityPatrolInterval
	}
	if c.StartupJitter < 0 {
		c.StartupJitter = 0
	}
	if c.MinAccountInterval <= 0 {
		c.MinAccountInterval = defaultBuddyActivityPatrolMinAccountInterval
	}
	if c.AccountJitter < 0 {
		c.AccountJitter = 0
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultBuddyActivityPatrolRequestTimeout
	}
	if strings.TrimSpace(c.Model) == "" {
		c.Model = DefaultBuddyActivityPatrolModel
	}
}
