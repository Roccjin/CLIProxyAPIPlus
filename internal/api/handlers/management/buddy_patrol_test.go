package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestGetBuddyPatrol_DefaultsAndAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		BuddyCreditsPatrol:  config.DefaultBuddyCreditsPatrolConfig(),
		BuddyActivityPatrol: config.DefaultBuddyActivityPatrolConfig(),
	}
	cb := &coreauth.Auth{
		ID:       "cb-" + uuid.NewString(),
		Provider: "codebuddy",
		FileName: "alice.json",
		Disabled: true,
		Metadata: map[string]any{
			"email":                                  "alice@example.com",
			"domain":                                 "www.codebuddy.ai",
			"type":                                   "codebuddy",
			coreauth.MetadataKeyDisabledReason:       coreauth.DisabledReasonCreditsExhausted,
			coreauth.MetadataKeyCreditsPatrolAt:      "2026-09-21T01:00:00Z",
			coreauth.MetadataKeyCreditsPatrolResult:  coreauth.CreditsPatrolResultStillEmpty,
			coreauth.MetadataKeyCreditsPatrolRemain:  0.0,
			coreauth.MetadataKeyActivityPatrolAt:     "2026-09-21T02:00:00Z",
			coreauth.MetadataKeyActivityPatrolResult: coreauth.ActivityPatrolResultOK,
		},
	}
	wb := &coreauth.Auth{
		ID:       "wb-" + uuid.NewString(),
		Provider: "workbuddy",
		FileName: "bob.json",
		Metadata: map[string]any{
			"email":  "bob@example.com",
			"domain": "www.workbuddy.ai",
			"type":   "workbuddy",
		},
	}
	other := &coreauth.Auth{
		ID:       "gem-" + uuid.NewString(),
		Provider: "gemini",
		FileName: "gemini.json",
	}
	manager := coreauth.NewManager(nil, nil, nil)
	for _, auth := range []*coreauth.Auth{cb, wb, other} {
		if _, err := manager.Register(coreauth.WithSkipPersist(context.Background()), auth); err != nil {
			t.Fatalf("register %s: %v", auth.ID, err)
		}
	}

	h := &Handler{cfg: cfg, authManager: manager, configFilePath: writeTestConfigFile(t)}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/buddy-patrol", nil)
	h.GetBuddyPatrol(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	credits, _ := payload["credits"].(map[string]any)
	if credits["enabled"] != true || credits["interval"] != "12h" {
		t.Fatalf("credits = %#v", credits)
	}
	activity, _ := payload["activity"].(map[string]any)
	if activity["enabled"] != true || activity["interval"] != "24h" || activity["model"] != "deepseek-v4.1-flash" {
		t.Fatalf("activity = %#v", activity)
	}
	accounts, _ := payload["accounts"].([]any)
	if len(accounts) != 2 {
		t.Fatalf("accounts = %#v", accounts)
	}
	var sawAlice bool
	for _, raw := range accounts {
		row, _ := raw.(map[string]any)
		if row["email"] != "alice@example.com" {
			continue
		}
		sawAlice = true
		if row["activity_eligible"] != true || row["region"] != "global" {
			t.Fatalf("alice = %#v", row)
		}
		creditsLast, _ := row["credits"].(map[string]any)
		if creditsLast["result"] != coreauth.CreditsPatrolResultStillEmpty {
			t.Fatalf("alice credits = %#v", creditsLast)
		}
		if creditsLast["remain"] != float64(0) {
			t.Fatalf("alice remain = %#v", creditsLast["remain"])
		}
		activityLast, _ := row["activity"].(map[string]any)
		if activityLast["result"] != coreauth.ActivityPatrolResultOK {
			t.Fatalf("alice activity = %#v", activityLast)
		}
	}
	if !sawAlice {
		t.Fatal("missing alice account")
	}
}

func TestPatchBuddyPatrol_UpdatesSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		BuddyCreditsPatrol:  config.DefaultBuddyCreditsPatrolConfig(),
		BuddyActivityPatrol: config.DefaultBuddyActivityPatrolConfig(),
	}
	h := &Handler{cfg: cfg, configFilePath: writeTestConfigFile(t)}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := `{"credits":{"enabled":false,"interval":"6h"},"activity":{"interval":"12h","model":"hy3"}}`
	c.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/buddy-patrol", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.PatchBuddyPatrol(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if cfg.BuddyCreditsPatrol.Enabled {
		t.Fatal("credits patrol still enabled")
	}
	if cfg.BuddyCreditsPatrol.Interval != 6*time.Hour {
		t.Fatalf("credits interval = %s", cfg.BuddyCreditsPatrol.Interval)
	}
	if cfg.BuddyActivityPatrol.Interval != 12*time.Hour {
		t.Fatalf("activity interval = %s", cfg.BuddyActivityPatrol.Interval)
	}
	if cfg.BuddyActivityPatrol.Model != "deepseek-v4.1-flash" {
		t.Fatalf("model = %q", cfg.BuddyActivityPatrol.Model)
	}
}

func TestPatchBuddyPatrol_RejectsInvalidDuration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		BuddyCreditsPatrol: config.DefaultBuddyCreditsPatrolConfig(),
	}
	h := &Handler{cfg: cfg, configFilePath: writeTestConfigFile(t)}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/buddy-patrol", strings.NewReader(`{"credits":{"interval":"nope"}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.PatchBuddyPatrol(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if cfg.BuddyCreditsPatrol.Interval != 12*time.Hour {
		t.Fatalf("interval mutated = %s", cfg.BuddyCreditsPatrol.Interval)
	}
}

func TestApplyBuddyPatrolPatch_EmptyModel(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{BuddyActivityPatrol: config.DefaultBuddyActivityPatrolConfig()}
	empty := ""
	err := applyBuddyPatrolPatch(cfg, buddyPatrolPatchBody{Activity: &buddyPatrolPatchFields{Model: &empty}})
	if err == nil {
		t.Fatal("expected empty model to fail")
	}
}
