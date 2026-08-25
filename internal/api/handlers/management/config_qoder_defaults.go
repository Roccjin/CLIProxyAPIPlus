package management

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	entries, err := parseQoderModelDefaultsBody(data)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	h.cfg.QoderModelDefaults = entries
	h.cfg.SanitizeQoderModelDefaults()
	h.persist(c)
}

// parseQoderModelDefaultsBody accepts either a wrapped management payload
// {"qoder-model-defaults":{"dfmodel":{"context":"1M"}}} or a raw model map.
// The wrapper must be preferred: unmarshalling the wrapped object into
// map[string]QoderModelDefault succeeds with a dummy empty entry because
// unknown JSON fields on the struct are ignored.
func parseQoderModelDefaultsBody(data []byte) (map[string]config.QoderModelDefault, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return map[string]config.QoderModelDefault{}, nil
	}
	var wrapper struct {
		Items json.RawMessage `json:"qoder-model-defaults"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("invalid body: %w", err)
	}
	payload := data
	if trimmed := bytes.TrimSpace(wrapper.Items); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		payload = trimmed
	}
	var entries map[string]config.QoderModelDefault
	if err := json.Unmarshal(payload, &entries); err != nil {
		return nil, fmt.Errorf("invalid body: %w", err)
	}
	if entries == nil {
		entries = map[string]config.QoderModelDefault{}
	}
	delete(entries, "qoder-model-defaults")
	return entries, nil
}
