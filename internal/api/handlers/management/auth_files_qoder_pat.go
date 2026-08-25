package management

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	qoderauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qoder"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// RequestQoderPAT exchanges a Qoder personal access token for a COSY session
// and writes a qoder auth file.
func (h *Handler) RequestQoderPAT(c *gin.Context) {
	ctx := context.Background()
	ctx = PopulateAuthContext(ctx, c)

	var payload struct {
		PAT string `json:"pat"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid body"})
		return
	}
	pat := strings.TrimSpace(payload.PAT)
	if pat == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "pat is required"})
		return
	}

	authSvc := qoderauth.NewQoderAuth(h.cfg)
	storage, err := authSvc.LoginWithPAT(ctx, pat)
	if err != nil {
		log.Errorf("qoder PAT login failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": err.Error()})
		return
	}

	fileName := qoderauth.PATAuthFileName(storage.Email, pat)
	label := storage.Name
	if label == "" {
		label = storage.Email
	}
	record := &coreauth.Auth{
		ID:       fileName,
		Provider: "qoder",
		FileName: fileName,
		Label:    label,
		Storage:  storage,
		Metadata: map[string]any{
			"email":     storage.Email,
			"name":      storage.Name,
			"user_id":   storage.UserID,
			"auth_mode": qoderauth.AuthModePAT,
		},
	}
	savedPath, errSave := h.saveTokenRecord(ctx, record)
	if errSave != nil {
		log.Errorf("Failed to save Qoder PAT: %v", errSave)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "failed to save authentication tokens"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"auth-file": savedPath,
		"email":     storage.Email,
		"name":      storage.Name,
		"pat":       qoderauth.MaskPAT(pat),
		"message":   fmt.Sprintf("Qoder PAT saved as %s", fileName),
	})
}
