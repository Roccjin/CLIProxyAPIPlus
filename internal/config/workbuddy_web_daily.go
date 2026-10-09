package config

import (
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultWorkBuddyWebDailyInterval           = 24 * time.Hour
	defaultWorkBuddyWebDailyStartupJitter      = 10 * time.Minute
	defaultWorkBuddyWebDailyMinAccountInterval = 45 * time.Second
	defaultWorkBuddyWebDailyAccountJitter      = 30 * time.Second
	defaultWorkBuddyWebDailyRequestTimeout     = 180 * time.Second
	// DefaultWorkBuddyWebDailyModel is the model on the web conversation that
	// the international daily activity reward counts.
	DefaultWorkBuddyWebDailyModel = "deepseek-v4.1-flash"
)

// WorkBuddyWebDailyConfig controls the background loop that opens one
// international WorkBuddy web conversation per credential and drives it until
// the session status is completed. A desktop chat completion does not settle
// the daily activity reward, so this loop never calls /v2/chat/completions.
type WorkBuddyWebDailyConfig struct {
	// Enabled starts the loop. Default true.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Interval is the minimum time between successful runs for one account. Default 24h.
	Interval time.Duration `yaml:"interval" json:"interval"`
	// StartupJitter is the maximum random delay before the first round. Default 10m.
	StartupJitter time.Duration `yaml:"startup-jitter" json:"startup-jitter"`
	// MinAccountInterval is the minimum pause between two accounts. Default 45s.
	MinAccountInterval time.Duration `yaml:"min-account-interval" json:"min-account-interval"`
	// AccountJitter is extra random delay added after MinAccountInterval. Default 30s.
	AccountJitter time.Duration `yaml:"account-jitter" json:"account-jitter"`
	// RequestTimeout bounds one account's create, sandbox, and completion wait. Default 180s.
	RequestTimeout time.Duration `yaml:"request-timeout" json:"request-timeout"`
	// Model is the web conversation model. Default deepseek-v4.1-flash.
	Model string `yaml:"model" json:"model"`
}

// DefaultWorkBuddyWebDailyConfig returns the default-on daily web conversation task.
func DefaultWorkBuddyWebDailyConfig() WorkBuddyWebDailyConfig {
	return WorkBuddyWebDailyConfig{
		Enabled:            true,
		Interval:           defaultWorkBuddyWebDailyInterval,
		StartupJitter:      defaultWorkBuddyWebDailyStartupJitter,
		MinAccountInterval: defaultWorkBuddyWebDailyMinAccountInterval,
		AccountJitter:      defaultWorkBuddyWebDailyAccountJitter,
		RequestTimeout:     defaultWorkBuddyWebDailyRequestTimeout,
		Model:              DefaultWorkBuddyWebDailyModel,
	}
}

// Normalize fills invalid duration and model values with defaults.
// Enabled is left unchanged so an explicit false remains off.
func (c *WorkBuddyWebDailyConfig) Normalize() {
	if c == nil {
		return
	}
	if c.Interval <= 0 {
		c.Interval = defaultWorkBuddyWebDailyInterval
	}
	if c.StartupJitter < 0 {
		c.StartupJitter = 0
	}
	if c.MinAccountInterval <= 0 {
		c.MinAccountInterval = defaultWorkBuddyWebDailyMinAccountInterval
	}
	if c.AccountJitter < 0 {
		c.AccountJitter = 0
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultWorkBuddyWebDailyRequestTimeout
	}
	if strings.TrimSpace(c.Model) == "" {
		c.Model = DefaultWorkBuddyWebDailyModel
	} else {
		c.Model = strings.TrimSpace(c.Model)
	}
}

type workBuddyWebDailyYAML struct {
	Enabled            *bool     `yaml:"enabled"`
	Interval           yaml.Node `yaml:"interval"`
	StartupJitter      yaml.Node `yaml:"startup-jitter"`
	MinAccountInterval yaml.Node `yaml:"min-account-interval"`
	AccountJitter      yaml.Node `yaml:"account-jitter"`
	RequestTimeout     yaml.Node `yaml:"request-timeout"`
	Model              *string   `yaml:"model"`
}

func (c *WorkBuddyWebDailyConfig) UnmarshalYAML(value *yaml.Node) error {
	if c == nil || value == nil {
		return nil
	}
	var raw workBuddyWebDailyYAML
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
	if d, ok, err := parseYAMLDurationNode(&raw.RequestTimeout); err != nil {
		return err
	} else if ok {
		c.RequestTimeout = d
	}
	if raw.Model != nil {
		c.Model = strings.TrimSpace(*raw.Model)
	}
	return nil
}

func (c WorkBuddyWebDailyConfig) MarshalYAML() (interface{}, error) {
	return map[string]interface{}{
		"enabled":              c.Enabled,
		"interval":             formatYAMLDuration(c.Interval),
		"startup-jitter":       formatYAMLDuration(c.StartupJitter),
		"min-account-interval": formatYAMLDuration(c.MinAccountInterval),
		"account-jitter":       formatYAMLDuration(c.AccountJitter),
		"request-timeout":      formatYAMLDuration(c.RequestTimeout),
		"model":                c.Model,
	}, nil
}
