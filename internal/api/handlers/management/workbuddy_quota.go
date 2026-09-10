package management

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
)

// GetWorkBuddyQuota fetches live WorkBuddy credits for one auth file.
func (h *Handler) GetWorkBuddyQuota(c *gin.Context) {
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
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "workbuddy") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not a workbuddy auth file"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	info, err := executor.FetchWorkBuddyQuota(ctx, auth, h.cfg)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": info})
}
