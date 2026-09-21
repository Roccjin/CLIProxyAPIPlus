package management

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type buddyPatrolSettingsView struct {
	Enabled            bool    `json:"enabled"`
	Interval           string  `json:"interval"`
	StartupJitter      string  `json:"startup-jitter"`
	MinAccountInterval string  `json:"min-account-interval"`
	AccountJitter      string  `json:"account-jitter"`
	MinRemain          float64 `json:"min-remain,omitempty"`
	RequestTimeout     string  `json:"request-timeout"`
	Model              string  `json:"model,omitempty"`
}

type buddyPatrolLastView struct {
	At     string   `json:"at,omitempty"`
	Result string   `json:"result,omitempty"`
	Remain *float64 `json:"remain,omitempty"`
}

type buddyPatrolAccountView struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	Email            string               `json:"email,omitempty"`
	Provider         string               `json:"provider"`
	Disabled         bool                 `json:"disabled"`
	DisabledReason   string               `json:"disabled_reason,omitempty"`
	Region           string               `json:"region,omitempty"`
	ActivityEligible bool                 `json:"activity_eligible"`
	Credits          *buddyPatrolLastView `json:"credits,omitempty"`
	Activity         *buddyPatrolLastView `json:"activity,omitempty"`
}

type buddyPatrolPatchFields struct {
	Enabled            *bool    `json:"enabled"`
	Interval           *string  `json:"interval"`
	StartupJitter      *string  `json:"startup-jitter"`
	MinAccountInterval *string  `json:"min-account-interval"`
	AccountJitter      *string  `json:"account-jitter"`
	MinRemain          *float64 `json:"min-remain"`
	RequestTimeout     *string  `json:"request-timeout"`
	Model              *string  `json:"model"`
}

type buddyPatrolPatchBody struct {
	Credits  *buddyPatrolPatchFields `json:"credits"`
	Activity *buddyPatrolPatchFields `json:"activity"`
}

func (h *Handler) GetBuddyPatrol(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config unavailable"})
		return
	}
	c.JSON(http.StatusOK, h.buddyPatrolSnapshot())
}

func (h *Handler) PatchBuddyPatrol(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config unavailable"})
		return
	}
	var body buddyPatrolPatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if body.Credits == nil && body.Activity == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "credits or activity is required"})
		return
	}
	if err := applyBuddyPatrolPatch(h.cfg, body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.persist(c)
}

func (h *Handler) buddyPatrolSnapshot() gin.H {
	credits := h.cfg.BuddyCreditsPatrol
	credits.Normalize()
	activity := h.cfg.BuddyActivityPatrol
	activity.Normalize()
	return gin.H{
		"home-mode": h.cfg.Home.Enabled,
		"credits": buddyPatrolSettingsView{
			Enabled:            credits.Enabled,
			Interval:           formatPatrolDuration(credits.Interval),
			StartupJitter:      formatPatrolDuration(credits.StartupJitter),
			MinAccountInterval: formatPatrolDuration(credits.MinAccountInterval),
			AccountJitter:      formatPatrolDuration(credits.AccountJitter),
			MinRemain:          credits.MinRemain,
			RequestTimeout:     formatPatrolDuration(credits.RequestTimeout),
		},
		"activity": buddyPatrolSettingsView{
			Enabled:            activity.Enabled,
			Interval:           formatPatrolDuration(activity.Interval),
			StartupJitter:      formatPatrolDuration(activity.StartupJitter),
			MinAccountInterval: formatPatrolDuration(activity.MinAccountInterval),
			AccountJitter:      formatPatrolDuration(activity.AccountJitter),
			RequestTimeout:     formatPatrolDuration(activity.RequestTimeout),
			Model:              activity.Model,
		},
		"accounts": h.buddyPatrolAccounts(),
	}
}

func (h *Handler) buddyPatrolAccounts() []buddyPatrolAccountView {
	if h == nil || h.authManager == nil {
		return []buddyPatrolAccountView{}
	}
	auths := h.authManager.List()
	out := make([]buddyPatrolAccountView, 0)
	for _, auth := range auths {
		if view, ok := buddyPatrolAccountFromAuth(auth); ok {
			out = append(out, view)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		left := strings.ToLower(out[i].Email)
		right := strings.ToLower(out[j].Email)
		if left == "" {
			left = strings.ToLower(out[i].Name)
		}
		if right == "" {
			right = strings.ToLower(out[j].Name)
		}
		if left != right {
			return left < right
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func buddyPatrolAccountFromAuth(auth *coreauth.Auth) (buddyPatrolAccountView, bool) {
	if auth == nil || coreauth.IsPluginVirtualAuth(auth) {
		return buddyPatrolAccountView{}, false
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	if provider != "workbuddy" && provider != "codebuddy" {
		return buddyPatrolAccountView{}, false
	}
	name := strings.TrimSpace(auth.FileName)
	if name == "" {
		name = strings.TrimSpace(auth.ID)
	}
	domain := authMetadataString(auth, "domain")
	region := "cn"
	if provider == "codebuddy" && codebuddy.IsGlobalDomain(domain) {
		region = "global"
	}
	if provider == "workbuddy" && workbuddy.IsGlobalDomain(domain) {
		region = "global"
	}
	view := buddyPatrolAccountView{
		ID:               strings.TrimSpace(auth.ID),
		Name:             name,
		Email:            authEmail(auth),
		Provider:         provider,
		Disabled:         auth.IsDisabled(),
		DisabledReason:   authMetadataString(auth, coreauth.MetadataKeyDisabledReason),
		Region:           region,
		ActivityEligible: provider == "codebuddy" && codebuddy.IsGlobalDomain(domain),
	}
	if credits := buddyPatrolCreditsView(auth); credits != nil {
		view.Credits = credits
	}
	if activity := buddyPatrolActivityView(auth); activity != nil {
		view.Activity = activity
	}
	return view, true
}

func buddyPatrolCreditsView(auth *coreauth.Auth) *buddyPatrolLastView {
	at := authMetadataString(auth, coreauth.MetadataKeyCreditsPatrolAt)
	result := authMetadataString(auth, coreauth.MetadataKeyCreditsPatrolResult)
	remain, remainOK := metadataFloatValue(auth, coreauth.MetadataKeyCreditsPatrolRemain)
	if at == "" && result == "" && !remainOK {
		return nil
	}
	view := &buddyPatrolLastView{At: at, Result: result}
	if remainOK {
		remainCopy := remain
		view.Remain = &remainCopy
	}
	return view
}

func buddyPatrolActivityView(auth *coreauth.Auth) *buddyPatrolLastView {
	at := authMetadataString(auth, coreauth.MetadataKeyActivityPatrolAt)
	result := authMetadataString(auth, coreauth.MetadataKeyActivityPatrolResult)
	if at == "" && result == "" {
		return nil
	}
	return &buddyPatrolLastView{At: at, Result: result}
}

func applyBuddyPatrolPatch(cfg *config.Config, body buddyPatrolPatchBody) error {
	if cfg == nil {
		return fmt.Errorf("config unavailable")
	}
	if body.Credits != nil {
		if err := applyCreditsPatrolPatch(&cfg.BuddyCreditsPatrol, body.Credits); err != nil {
			return err
		}
	}
	if body.Activity != nil {
		if err := applyActivityPatrolPatch(&cfg.BuddyActivityPatrol, body.Activity); err != nil {
			return err
		}
	}
	cfg.BuddyCreditsPatrol.Normalize()
	cfg.BuddyActivityPatrol.Normalize()
	return nil
}

func applyCreditsPatrolPatch(cfg *config.BuddyCreditsPatrolConfig, patch *buddyPatrolPatchFields) error {
	if cfg == nil || patch == nil {
		return nil
	}
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.Interval != nil {
		d, err := parseRequiredPatrolDuration("interval", *patch.Interval)
		if err != nil {
			return err
		}
		cfg.Interval = d
	}
	if patch.StartupJitter != nil {
		d, err := parseOptionalPatrolDuration("startup-jitter", *patch.StartupJitter)
		if err != nil {
			return err
		}
		cfg.StartupJitter = d
	}
	if patch.MinAccountInterval != nil {
		d, err := parseRequiredPatrolDuration("min-account-interval", *patch.MinAccountInterval)
		if err != nil {
			return err
		}
		cfg.MinAccountInterval = d
	}
	if patch.AccountJitter != nil {
		d, err := parseOptionalPatrolDuration("account-jitter", *patch.AccountJitter)
		if err != nil {
			return err
		}
		cfg.AccountJitter = d
	}
	if patch.MinRemain != nil {
		if *patch.MinRemain < 0 {
			return fmt.Errorf("min-remain must be >= 0")
		}
		cfg.MinRemain = *patch.MinRemain
	}
	if patch.RequestTimeout != nil {
		d, err := parseRequiredPatrolDuration("request-timeout", *patch.RequestTimeout)
		if err != nil {
			return err
		}
		cfg.RequestTimeout = d
	}
	return nil
}

func applyActivityPatrolPatch(cfg *config.BuddyActivityPatrolConfig, patch *buddyPatrolPatchFields) error {
	if cfg == nil || patch == nil {
		return nil
	}
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.Interval != nil {
		d, err := parseRequiredPatrolDuration("interval", *patch.Interval)
		if err != nil {
			return err
		}
		cfg.Interval = d
	}
	if patch.StartupJitter != nil {
		d, err := parseOptionalPatrolDuration("startup-jitter", *patch.StartupJitter)
		if err != nil {
			return err
		}
		cfg.StartupJitter = d
	}
	if patch.MinAccountInterval != nil {
		d, err := parseRequiredPatrolDuration("min-account-interval", *patch.MinAccountInterval)
		if err != nil {
			return err
		}
		cfg.MinAccountInterval = d
	}
	if patch.AccountJitter != nil {
		d, err := parseOptionalPatrolDuration("account-jitter", *patch.AccountJitter)
		if err != nil {
			return err
		}
		cfg.AccountJitter = d
	}
	if patch.RequestTimeout != nil {
		d, err := parseRequiredPatrolDuration("request-timeout", *patch.RequestTimeout)
		if err != nil {
			return err
		}
		cfg.RequestTimeout = d
	}
	if patch.Model != nil {
		model := strings.TrimSpace(*patch.Model)
		if model == "" {
			return fmt.Errorf("model must not be empty")
		}
		cfg.Model = model
	}
	return nil
}

func parseRequiredPatrolDuration(field, raw string) (time.Duration, error) {
	d, err := parseOptionalPatrolDuration(field, raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", field)
	}
	return d, nil
}

func parseOptionalPatrolDuration(field, raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("%s must not be empty", field)
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s is not a valid duration", field)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s must be >= 0", field)
	}
	return d, nil
}

func formatPatrolDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}

func metadataFloatValue(auth *coreauth.Auth, key string) (float64, bool) {
	if auth == nil || auth.Metadata == nil || strings.TrimSpace(key) == "" {
		return 0, false
	}
	raw, ok := auth.Metadata[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return n, true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}
