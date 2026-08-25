package management

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func (h *Handler) GetQoderModelDefaults(c *gin.Context) {
	defaults := h.cfg.QoderModelDefaults
	if defaults == nil {
		defaults = map[string]config.QoderModelDefault{}
	}
	c.JSON(http.StatusOK, gin.H{"qoder-model-defaults": defaults})
}

func (h *Handler) PutQoderModelDefaults(c *gin.Context) {
	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	var entries map[string]config.QoderModelDefault
	if err = json.Unmarshal(data, &entries); err != nil {
		var wrapper struct {
			Items map[string]config.QoderModelDefault `json:"qoder-model-defaults"`
		}
		if err2 := json.Unmarshal(data, &wrapper); err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		entries = wrapper.Items
	}
	h.cfg.QoderModelDefaults = entries
	h.cfg.SanitizeQoderModelDefaults()
	h.persist(c)
}
