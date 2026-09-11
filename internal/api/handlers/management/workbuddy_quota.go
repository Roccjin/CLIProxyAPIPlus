package management

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
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

	ctx := c.Request.Context()
	accessBefore, refreshBefore := workBuddyAuthTokens(auth)
	info, err := executor.FetchWorkBuddyQuota(ctx, auth, h.cfg)
	h.persistWorkBuddyAuthIfRefreshed(ctx, auth, accessBefore, refreshBefore)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": info})
}

func workBuddyAuthTokens(auth *coreauth.Auth) (accessToken, refreshToken string) {
	if auth == nil || auth.Metadata == nil {
		return "", ""
	}
	accessToken, _ = auth.Metadata["access_token"].(string)
	refreshToken, _ = auth.Metadata["refresh_token"].(string)
	return strings.TrimSpace(accessToken), strings.TrimSpace(refreshToken)
}

func (h *Handler) persistWorkBuddyAuthIfRefreshed(ctx context.Context, auth *coreauth.Auth, accessBefore, refreshBefore string) {
	if h == nil || h.authManager == nil || auth == nil {
		return
	}
	accessAfter, refreshAfter := workBuddyAuthTokens(auth)
	if accessAfter == accessBefore && refreshAfter == refreshBefore {
		return
	}
	if _, errUpdate := h.authManager.Update(ctx, auth); errUpdate != nil {
		log.Warnf("workbuddy quota: persist refreshed credentials failed: %v", errUpdate)
	}
}
