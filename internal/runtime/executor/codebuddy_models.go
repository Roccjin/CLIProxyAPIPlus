package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const codeBuddyConfigPath = "/v3/config"

// FetchCodeBuddyModels retrieves the live CLI catalog from /v3/config.
// Falls back to the static CN or international list when credentials are
// missing or the upstream catalog cannot be parsed.
func FetchCodeBuddyModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) []*registry.ModelInfo {
	_, _, domain := codeBuddyCredentials(auth)
	fallback := codeBuddyFallbackModels(domain)
	models, err := fetchCodeBuddyModelsLive(ctx, auth, cfg)
	if err != nil {
		log.Warnf("codebuddy: %v", err)
		return fallback
	}
	if len(models) == 0 {
		return fallback
	}
	return models
}

// RefreshCodeBuddyModels live-fetches /v3/config and returns an error instead
// of falling back to the static catalog. Used by the management "refresh
// supported models" action.
func RefreshCodeBuddyModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) ([]*registry.ModelInfo, error) {
	accessToken, _, _ := codeBuddyCredentials(auth)
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("codebuddy: missing access token")
	}
	models, err := fetchCodeBuddyModelsLive(ctx, auth, cfg)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("codebuddy: upstream returned no cli models")
	}
	return models, nil
}

func fetchCodeBuddyModelsLive(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) ([]*registry.ModelInfo, error) {
	accessToken, userID, domain := codeBuddyCredentials(auth)
	if accessToken == "" {
		return nil, fmt.Errorf("missing access token")
	}
	url := codebuddy.APIBaseURLForDomain(domain) + codeBuddyConfigPath
	return fetchCodeBuddyModelsFromURL(ctx, auth, cfg, url, accessToken, userID, domain)
}

func fetchCodeBuddyModelsFromURL(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, modelsURL, accessToken, userID, domain string) ([]*registry.ModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build v3 config request: %w", err)
	}
	e := NewCodeBuddyExecutor(cfg)
	e.applyHeaders(req, accessToken, userID, domain)
	req.Header.Set("Accept", "application/json, text/plain, */*")

	httpClient := newProxyAwareHTTPClient(fetchCtx, cfg, auth, 0)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch v3 config failed: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("codebuddy: close v3 config body error: %v", errClose)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read v3 config: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("v3 config status %d", resp.StatusCode)
	}

	models, err := parseCodeBuddyV3CLIModels(body)
	if err != nil {
		return nil, fmt.Errorf("parse v3 config: %w", err)
	}
	return models, nil
}

func codeBuddyFallbackModels(domain string) []*registry.ModelInfo {
	if codebuddy.IsGlobalDomain(domain) {
		return registry.GetCodeBuddyGlobalModels()
	}
	return registry.GetCodeBuddyModels()
}

func parseCodeBuddyV3CLIModels(raw []byte) ([]*registry.ModelInfo, error) {
	var response struct {
		Code int `json:"code"`
		Data *struct {
			Agents []struct {
				Name   string   `json:"name"`
				Models []string `json:"models"`
			} `json:"agents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode v3 config: %w", err)
	}
	if response.Code != 0 {
		return nil, fmt.Errorf("v3 config business code %d", response.Code)
	}
	if response.Data == nil {
		return nil, fmt.Errorf("v3 config data is missing")
	}

	var modelIDs []string
	foundCLI := false
	for _, agent := range response.Data.Agents {
		if agent.Name != "cli" {
			continue
		}
		if foundCLI {
			return nil, fmt.Errorf("v3 config has multiple cli agents")
		}
		foundCLI = true
		modelIDs = agent.Models
	}
	if !foundCLI {
		return nil, fmt.Errorf("v3 config cli agent is missing")
	}

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
		models = append(models, registry.NewCodeBuddyModelInfo(id, now))
	}
	return models, nil
}
