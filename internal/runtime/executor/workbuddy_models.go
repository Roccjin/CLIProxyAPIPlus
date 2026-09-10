package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const workBuddyConfigPath = "/v3/config"

// FetchWorkBuddyModels retrieves the live CLI catalog from /v3/config.
// Falls back to the static CN or international list when credentials are
// missing or the upstream catalog cannot be parsed.
func FetchWorkBuddyModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) []*registry.ModelInfo {
	_, _, domain := workBuddyCredentials(auth)
	fallback := workBuddyFallbackModels(domain)
	models, err := fetchWorkBuddyModelsLive(ctx, auth, cfg)
	if err != nil {
		log.Warnf("workbuddy: %v", err)
		return fallback
	}
	if len(models) == 0 {
		return fallback
	}
	return models
}

// RefreshWorkBuddyModels live-fetches /v3/config and returns an error instead
// of falling back to the static catalog. Used by the management "refresh
// supported models" action.
func RefreshWorkBuddyModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) ([]*registry.ModelInfo, error) {
	accessToken, _, _ := workBuddyCredentials(auth)
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("workbuddy: missing access token")
	}
	models, err := fetchWorkBuddyModelsLive(ctx, auth, cfg)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("workbuddy: upstream returned no cli models")
	}
	return models, nil
}

func fetchWorkBuddyModelsLive(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) ([]*registry.ModelInfo, error) {
	accessToken, userID, domain := workBuddyCredentials(auth)
	if accessToken == "" {
		return nil, fmt.Errorf("missing access token")
	}
	url := workbuddy.APIBaseURLForDomain(domain) + workBuddyConfigPath
	return fetchWorkBuddyModelsFromURL(ctx, auth, cfg, url, accessToken, userID, domain)
}

func fetchWorkBuddyModelsFromURL(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, modelsURL, accessToken, userID, domain string) ([]*registry.ModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build v3 config request: %w", err)
	}
	e := NewWorkBuddyExecutor(cfg)
	e.applyHeaders(req, accessToken, userID, domain)
	req.Header.Set("Accept", "application/json, text/plain, */*")

	httpClient := newProxyAwareHTTPClient(fetchCtx, cfg, auth, 0)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch v3 config failed: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("workbuddy: close v3 config body error: %v", errClose)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read v3 config: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("v3 config status %d", resp.StatusCode)
	}

	models, err := parseWorkBuddyV3CLIModels(body)
	if err != nil {
		return nil, fmt.Errorf("parse v3 config: %w", err)
	}
	return models, nil
}

func workBuddyFallbackModels(domain string) []*registry.ModelInfo {
	if workbuddy.IsGlobalDomain(domain) {
		return registry.GetWorkBuddyGlobalModels()
	}
	return registry.GetWorkBuddyModels()
}

func parseWorkBuddyV3CLIModels(raw []byte) ([]*registry.ModelInfo, error) {
	if !gjson.ValidBytes(raw) {
		return nil, fmt.Errorf("decode v3 config: invalid json")
	}
	root := gjson.ParseBytes(raw)
	if code := root.Get("code"); code.Exists() && !workBuddyV3CodeOK(code) {
		return nil, fmt.Errorf("v3 config business code %s", strings.TrimSpace(code.Raw))
	}
	data := root.Get("data")
	if !data.Exists() || !data.IsObject() {
		return nil, fmt.Errorf("v3 config data is missing")
	}

	modelIDs, err := parseWorkBuddyV3CLIAgentIDs(data.Get("agents"))
	if err != nil {
		return nil, err
	}

	liveByID := parseWorkBuddyV3LiveModels(data.Get("models"))
	now := time.Now().Unix()
	models := make([]*registry.ModelInfo, 0, len(modelIDs))
	seen := make(map[string]struct{}, len(modelIDs))
	for _, id := range modelIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		info := registry.NewWorkBuddyModelInfo(id, now)
		if live, ok := liveByID[id]; ok {
			registry.ApplyWorkBuddyLiveMeta(info, live)
		}
		models = append(models, info)
	}
	return models, nil
}

func workBuddyV3CodeOK(code gjson.Result) bool {
	switch code.Type {
	case gjson.Number:
		return code.Int() == 0
	case gjson.String:
		value := strings.TrimSpace(code.String())
		return value == "" || value == "0"
	case gjson.Null:
		return true
	default:
		return false
	}
}

func parseWorkBuddyV3CLIAgentIDs(agents gjson.Result) ([]string, error) {
	if !agents.Exists() || !agents.IsArray() {
		return nil, fmt.Errorf("v3 config cli agent is missing")
	}
	var modelIDs []string
	foundCLI := false
	for _, agent := range agents.Array() {
		if agent.Get("name").String() != "cli" {
			continue
		}
		if foundCLI {
			return nil, fmt.Errorf("v3 config has multiple cli agents")
		}
		foundCLI = true
		models := agent.Get("models")
		if !models.IsArray() {
			continue
		}
		for _, id := range models.Array() {
			modelIDs = append(modelIDs, id.String())
		}
	}
	if !foundCLI {
		return nil, fmt.Errorf("v3 config cli agent is missing")
	}
	return modelIDs, nil
}

func parseWorkBuddyV3LiveModels(models gjson.Result) map[string]registry.WorkBuddyLiveMeta {
	if !models.Exists() || !models.IsArray() {
		return nil
	}
	liveByID := make(map[string]registry.WorkBuddyLiveMeta)
	for _, item := range models.Array() {
		id, meta, ok := parseWorkBuddyV3LiveModel(item)
		if !ok {
			continue
		}
		liveByID[id] = meta
	}
	return liveByID
}

func parseWorkBuddyV3LiveModel(item gjson.Result) (string, registry.WorkBuddyLiveMeta, bool) {
	if !item.IsObject() {
		return "", registry.WorkBuddyLiveMeta{}, false
	}
	id := strings.TrimSpace(item.Get("id").String())
	if id == "" {
		return "", registry.WorkBuddyLiveMeta{}, false
	}
	contextLength := int(item.Get("maxInputTokens").Int())
	if contextLength <= 0 {
		contextLength = int(item.Get("maxAllowedSize").Int())
	}
	meta := registry.WorkBuddyLiveMeta{
		DisplayName:         strings.TrimSpace(item.Get("name").String()),
		ContextLength:       contextLength,
		MaxCompletionTokens: int(item.Get("maxOutputTokens").Int()),
	}
	reasoning := item.Get("reasoning")
	supportsReasoning := item.Get("supportsReasoning").Bool()
	if !reasoning.Exists() || reasoning.Type == gjson.Null {
		if !supportsReasoning {
			meta.ClearThinking = true
		}
		return id, meta, true
	}
	if !reasoning.IsObject() {
		return id, meta, true
	}
	meta.HasReasoning = true
	if efforts := reasoning.Get("supportedEfforts"); efforts.IsArray() {
		for _, effort := range efforts.Array() {
			meta.Levels = append(meta.Levels, effort.String())
		}
	}
	meta.DefaultLevel = strings.TrimSpace(reasoning.Get("defaultEffort").String())
	if meta.DefaultLevel == "" {
		meta.DefaultLevel = strings.TrimSpace(reasoning.Get("effort").String())
	}
	if disable := reasoning.Get("canDisableThinking"); disable.Exists() && disable.Type != gjson.Null {
		meta.ZeroAllowed = disable.Bool()
	}
	return id, meta, true
}
