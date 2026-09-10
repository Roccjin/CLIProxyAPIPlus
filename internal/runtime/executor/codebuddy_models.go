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
	accessToken, userID, domain := codeBuddyCredentials(auth)
	fallback := codeBuddyFallbackModels(domain)
	if accessToken == "" {
		return fallback
	}
	if ctx == nil {
		ctx = context.Background()
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	url := codebuddy.APIBaseURLForDomain(domain) + codeBuddyConfigPath
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		log.Warnf("codebuddy: build v3 config request: %v", err)
		return fallback
	}
	e := NewCodeBuddyExecutor(cfg)
	e.applyHeaders(req, accessToken, userID, domain)
	req.Header.Set("Accept", "application/json, text/plain, */*")

	httpClient := newProxyAwareHTTPClient(fetchCtx, cfg, auth, 0)
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Warnf("codebuddy: fetch v3 config failed: %v", err)
		return fallback
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("codebuddy: close v3 config body error: %v", errClose)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Warnf("codebuddy: read v3 config: %v", err)
		return fallback
	}
	if resp.StatusCode != http.StatusOK {
		log.Warnf("codebuddy: v3 config status %d", resp.StatusCode)
		return fallback
	}

	models, err := parseCodeBuddyV3CLIModels(body)
	if err != nil {
		log.Warnf("codebuddy: parse v3 config: %v", err)
		return fallback
	}
	if len(models) == 0 {
		return fallback
	}
	return models
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
