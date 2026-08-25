package management

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
)

// GetQoderQuota fetches live Qoder quota/usage for one auth file.
func (h *Handler) GetQoderQuota(c *gin.Context) {
	name := strings.TrimSpace(c.Query("name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	auth, ok := h.lookupAuthFile(name, strings.TrimSpace(c.Query("auth_index")))
	if !ok || auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "qoder") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not a qoder auth file"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	info := executor.FetchQoderUsage(ctx, auth, h.cfg)
	if info == nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch qoder quota"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": qoderUsageFromAuth(auth)})
}
