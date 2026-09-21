package config

import (
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultBuddyCreditsPatrolInterval           = 12 * time.Hour
	defaultBuddyCreditsPatrolStartupJitter      = 10 * time.Minute
	defaultBuddyCreditsPatrolMinAccountInterval = 45 * time.Second
	defaultBuddyCreditsPatrolAccountJitter      = 30 * time.Second
	defaultBuddyCreditsPatrolMinRemain          = 1
	defaultBuddyCreditsPatrolRequestTimeout     = 45 * time.Second
)

// BuddyCreditsPatrolConfig controls the background loop that re-enables
// WorkBuddy/CodeBuddy credentials after monthly credits are restored.
type BuddyCreditsPatrolConfig struct {
	// Enabled starts the patrol loop. Default true.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Interval is the minimum time between full patrol rounds. Default 12h.
	Interval time.Duration `yaml:"interval" json:"interval"`
	// StartupJitter is the maximum random delay before the first round. Default 10m.
	StartupJitter time.Duration `yaml:"startup-jitter" json:"startup-jitter"`
	// MinAccountInterval is the minimum pause between two quota requests. Default 45s.
	MinAccountInterval time.Duration `yaml:"min-account-interval" json:"min-account-interval"`
	// AccountJitter is extra random delay added after MinAccountInterval. Default 30s.
	AccountJitter time.Duration `yaml:"account-jitter" json:"account-jitter"`
	// MinRemain is the inclusive remaining-credits threshold for auto re-enable. Default 1.
	MinRemain float64 `yaml:"min-remain" json:"min-remain"`
	// RequestTimeout bounds a single quota fetch. Default 45s.
	RequestTimeout time.Duration `yaml:"request-timeout" json:"request-timeout"`
}

// DefaultBuddyCreditsPatrolConfig returns the default-on 12h serial patrol settings.
func DefaultBuddyCreditsPatrolConfig() BuddyCreditsPatrolConfig {
	return BuddyCreditsPatrolConfig{
		Enabled:            true,
		Interval:           defaultBuddyCreditsPatrolInterval,
		StartupJitter:      defaultBuddyCreditsPatrolStartupJitter,
		MinAccountInterval: defaultBuddyCreditsPatrolMinAccountInterval,
		AccountJitter:      defaultBuddyCreditsPatrolAccountJitter,
		MinRemain:          defaultBuddyCreditsPatrolMinRemain,
		RequestTimeout:     defaultBuddyCreditsPatrolRequestTimeout,
	}
}

// Normalize fills invalid duration/threshold values with defaults.
// Enabled is left unchanged so an explicit false remains off.
func (c *BuddyCreditsPatrolConfig) Normalize() {
	if c == nil {
		return
	}
	if c.Interval <= 0 {
		c.Interval = defaultBuddyCreditsPatrolInterval
	}
	if c.StartupJitter < 0 {
		c.StartupJitter = 0
	}
	if c.MinAccountInterval <= 0 {
		c.MinAccountInterval = defaultBuddyCreditsPatrolMinAccountInterval
	}
	if c.AccountJitter < 0 {
		c.AccountJitter = 0
	}
	if c.MinRemain < 0 {
		c.MinRemain = 0
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultBuddyCreditsPatrolRequestTimeout
	}
}

type buddyCreditsPatrolYAML struct {
	Enabled            *bool     `yaml:"enabled"`
	Interval           yaml.Node `yaml:"interval"`
	StartupJitter      yaml.Node `yaml:"startup-jitter"`
	MinAccountInterval yaml.Node `yaml:"min-account-interval"`
	AccountJitter      yaml.Node `yaml:"account-jitter"`
	MinRemain          *float64  `yaml:"min-remain"`
	RequestTimeout     yaml.Node `yaml:"request-timeout"`
}

func (c *BuddyCreditsPatrolConfig) UnmarshalYAML(value *yaml.Node) error {
	if c == nil || value == nil {
		return nil
	}
	var raw buddyCreditsPatrolYAML
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Enabled != nil {
		c.Enabled = *raw.Enabled
	}
	if d, ok, err := parseYAMLDurationNode(&raw.Interval); err != nil {
		return err
	} else if ok {
		c.Interval = d
	}
	if d, ok, err := parseYAMLDurationNode(&raw.StartupJitter); err != nil {
		return err
	} else if ok {
		c.StartupJitter = d
	}
	if d, ok, err := parseYAMLDurationNode(&raw.MinAccountInterval); err != nil {
		return err
	} else if ok {
		c.MinAccountInterval = d
	}
	if d, ok, err := parseYAMLDurationNode(&raw.AccountJitter); err != nil {
		return err
	} else if ok {
		c.AccountJitter = d
	}
	if raw.MinRemain != nil {
		c.MinRemain = *raw.MinRemain
	}
	if d, ok, err := parseYAMLDurationNode(&raw.RequestTimeout); err != nil {
		return err
	} else if ok {
		c.RequestTimeout = d
	}
	return nil
}

func (c BuddyCreditsPatrolConfig) MarshalYAML() (interface{}, error) {
	return map[string]interface{}{
		"enabled":              c.Enabled,
		"interval":             formatYAMLDuration(c.Interval),
		"startup-jitter":       formatYAMLDuration(c.StartupJitter),
		"min-account-interval": formatYAMLDuration(c.MinAccountInterval),
		"account-jitter":       formatYAMLDuration(c.AccountJitter),
		"min-remain":           c.MinRemain,
		"request-timeout":      formatYAMLDuration(c.RequestTimeout),
	}, nil
}
