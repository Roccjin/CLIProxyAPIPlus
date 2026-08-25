package management

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	qoderauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qoder"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
)

// GetQoderModels returns Qoder catalog rows for the management defaults UI.
// It prefers cached /algo/api/v2/model/list entries from PAT/OAuth files and
// falls back to the static ModelMap so context defaults can be set without
// a working /v1/models API key.
func (h *Handler) GetQoderModels(c *gin.Context) {
	models := h.collectQoderCatalog()
	if len(models) == 0 {
		models = executor.FallbackQoderCatalog()
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

func (h *Handler) collectQoderCatalog() []executor.QoderCatalogModel {
	if h == nil || h.authManager == nil {
		return nil
	}
	byKey := map[string]executor.QoderCatalogModel{}
	for _, auth := range h.authManager.List() {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "qoder") {
			continue
		}
		storage, ok := auth.Storage.(*qoderauth.QoderTokenStorage)
		if !ok || storage == nil {
			continue
		}
		for _, model := range executor.CollectQoderCatalog(storage) {
			if model.Key == "" {
				continue
			}
			byKey[model.Key] = model
		}
	}
	if len(byKey) == 0 {
		return nil
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]executor.QoderCatalogModel, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out
}
