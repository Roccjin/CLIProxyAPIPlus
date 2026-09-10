package management

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const (
	authFileModelsRefreshTimeout       = 45 * time.Second
	authFileModelsRefreshMaxParallel   = 4
	authFileModelsRefreshProviderQoder = "qoder"
	authFileModelsRefreshProviderCB    = "codebuddy"
)

// AuthModelsRefreshFunc live-fetches provider models for one auth and rebinds the registry.
type AuthModelsRefreshFunc func(ctx context.Context, auth *coreauth.Auth) ([]*registry.ModelInfo, error)

type authFileModelsRefreshItem struct {
	Name      string `json:"name"`
	AuthIndex string `json:"auth_index"`
}

type authFileModelsRefreshRequest struct {
	Name      string                      `json:"name"`
	AuthIndex string                      `json:"auth_index"`
	Names     []string                    `json:"names"`
	Files     []authFileModelsRefreshItem `json:"files"`
}

type authFileModelsRefreshFailure struct {
	Name      string `json:"name"`
	AuthIndex string `json:"auth_index,omitempty"`
	Error     string `json:"error"`
}

func isAuthFileModelsRefreshProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case authFileModelsRefreshProviderQoder, authFileModelsRefreshProviderCB:
		return true
	default:
		return false
	}
}

func authFileModelEntries(models []*registry.ModelInfo) []gin.H {
	result := make([]gin.H, 0, len(models))
	for _, model := range models {
		if model == nil || strings.TrimSpace(model.ID) == "" {
			continue
		}
		entry := gin.H{"id": model.ID}
		if model.DisplayName != "" {
			entry["display_name"] = model.DisplayName
		}
		if model.Type != "" {
			entry["type"] = model.Type
		}
		if model.OwnedBy != "" {
			entry["owned_by"] = model.OwnedBy
		}
		result = append(result, entry)
	}
	return result
}

func collectAuthFileModelsRefreshTargets(req authFileModelsRefreshRequest) []authFileModelsRefreshItem {
	if len(req.Files) > 0 {
		return dedupeAuthFileModelsRefreshItems(req.Files)
	}
	if len(req.Names) > 0 {
		items := make([]authFileModelsRefreshItem, 0, len(req.Names))
		for _, name := range req.Names {
			items = append(items, authFileModelsRefreshItem{Name: name})
		}
		return dedupeAuthFileModelsRefreshItems(items)
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil
	}
	return []authFileModelsRefreshItem{{Name: req.Name, AuthIndex: req.AuthIndex}}
}

func dedupeAuthFileModelsRefreshItems(items []authFileModelsRefreshItem) []authFileModelsRefreshItem {
	seen := make(map[string]struct{}, len(items))
	out := make([]authFileModelsRefreshItem, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		authIndex := strings.TrimSpace(item.AuthIndex)
		key := name + "\x00" + authIndex
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, authFileModelsRefreshItem{Name: name, AuthIndex: authIndex})
	}
	return out
}

func (h *Handler) refreshAuthFileModels(ctx context.Context, auth *coreauth.Auth) ([]*registry.ModelInfo, error) {
	if h != nil && h.authModelsRefreshHook != nil {
		return h.authModelsRefreshHook(ctx, auth)
	}
	return h.refreshAuthFileModelsDirect(ctx, auth)
}

func (h *Handler) refreshAuthFileModelsDirect(ctx context.Context, auth *coreauth.Auth) ([]*registry.ModelInfo, error) {
	if auth == nil {
		return nil, fmt.Errorf("auth file not found")
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	var (
		models []*registry.ModelInfo
		err    error
	)
	switch provider {
	case authFileModelsRefreshProviderQoder:
		models, err = executor.RefreshQoderModels(ctx, auth, h.cfg)
	case authFileModelsRefreshProviderCB:
		models, err = executor.RefreshCodeBuddyModels(ctx, auth, h.cfg)
	default:
		return nil, fmt.Errorf("model refresh is only supported for qoder and codebuddy")
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(auth.ID) != "" {
		if len(models) > 0 {
			registry.GetGlobalRegistry().RegisterClient(auth.ID, provider, models)
		} else {
			registry.GetGlobalRegistry().UnregisterClient(auth.ID)
		}
		models = registry.GetGlobalRegistry().GetModelsForClient(auth.ID)
	}
	return models, nil
}

// RefreshAuthFileModels live-refreshes supported models for one qoder/codebuddy auth file.
func (h *Handler) RefreshAuthFileModels(c *gin.Context) {
	var req authFileModelsRefreshRequest
	if errBindJSON := c.ShouldBindJSON(&req); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	targets := collectAuthFileModelsRefreshTargets(req)
	if len(targets) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	target := targets[0]

	auth, ok := h.lookupAuthFile(target.Name, target.AuthIndex)
	if !ok || auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
		return
	}
	if !isAuthFileModelsRefreshProvider(auth.Provider) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model refresh is only supported for qoder and codebuddy"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), authFileModelsRefreshTimeout)
	defer cancel()
	models, errRefresh := h.refreshAuthFileModels(ctx, auth)
	if errRefresh != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": errRefresh.Error()})
		return
	}
	entries := authFileModelEntries(models)
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"name":       target.Name,
		"auth_index": lockedAuthIndex(auth),
		"count":      len(entries),
		"models":     entries,
	})
}

// RefreshAuthFileModelsBatch live-refreshes supported models for selected qoder/codebuddy auth files.
func (h *Handler) RefreshAuthFileModelsBatch(c *gin.Context) {
	var req authFileModelsRefreshRequest
	if errBindJSON := c.ShouldBindJSON(&req); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	targets := collectAuthFileModelsRefreshTargets(req)
	if len(targets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "names is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), authFileModelsRefreshTimeout)
	defer cancel()

	type refreshResult struct {
		name      string
		authIndex string
		err       error
	}
	results := make([]refreshResult, len(targets))
	sem := make(chan struct{}, authFileModelsRefreshMaxParallel)
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(idx int, item authFileModelsRefreshItem) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[idx] = refreshResult{name: item.Name, authIndex: item.AuthIndex, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			auth, ok := h.lookupAuthFile(item.Name, item.AuthIndex)
			if !ok || auth == nil {
				results[idx] = refreshResult{name: item.Name, authIndex: item.AuthIndex, err: fmt.Errorf("auth file not found")}
				return
			}
			if !isAuthFileModelsRefreshProvider(auth.Provider) {
				results[idx] = refreshResult{
					name:      item.Name,
					authIndex: lockedAuthIndex(auth),
					err:       fmt.Errorf("model refresh is only supported for qoder and codebuddy"),
				}
				return
			}
			_, errRefresh := h.refreshAuthFileModels(ctx, auth)
			results[idx] = refreshResult{
				name:      item.Name,
				authIndex: lockedAuthIndex(auth),
				err:       errRefresh,
			}
		}(i, target)
	}
	wg.Wait()

	refreshed := make([]string, 0, len(results))
	failed := make([]authFileModelsRefreshFailure, 0)
	for _, result := range results {
		if result.err != nil {
			failed = append(failed, authFileModelsRefreshFailure{
				Name:      result.name,
				AuthIndex: result.authIndex,
				Error:     result.err.Error(),
			})
			continue
		}
		refreshed = append(refreshed, result.name)
	}

	status := "ok"
	if len(failed) > 0 && len(refreshed) > 0 {
		status = "partial"
	} else if len(failed) > 0 {
		status = "failed"
	}
	c.JSON(http.StatusOK, gin.H{
		"status":    status,
		"refreshed": len(refreshed),
		"files":     refreshed,
		"failed":    failed,
	})
}
