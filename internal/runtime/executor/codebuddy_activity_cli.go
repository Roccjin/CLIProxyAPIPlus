package executor

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	_ "embed"

	"github.com/google/uuid"
)

const (
	// codeBuddyCLIActivityModel is the model on the official CLI conversation
	// that qualifies for the international daily activity gift.
	codeBuddyCLIActivityModel   = "deepseek-v4.1-flash"
	codeBuddyCLIActivityQuery   = "<user_query>你好</user_query>"
	codeBuddyCLIReasoningEffort = "xhigh"
	codeBuddyCLIVerbosity       = "high"
)

//go:embed codebuddy_cli_system.txt
var codeBuddyCLISystemTemplate string

//go:embed codebuddy_cli_memory.txt
var codeBuddyCLIMemoryText string

//go:embed codebuddy_cli_rules.txt
var codeBuddyCLIRulesText string

//go:embed codebuddy_cli_tools.json
var codeBuddyCLIToolsJSON []byte

// codeBuddyCLIWire is the UUID v7 / W3C trace identity used by one CLI turn.
type codeBuddyCLIWire struct {
	codeBuddyChatIDs
	SpanID       string
	ParentSpanID string
}

func resolveCodeBuddyActivityModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.EqualFold(model, "hy3") {
		return codeBuddyCLIActivityModel
	}
	return model
}

func newCodeBuddyCLIWire() (codeBuddyCLIWire, error) {
	var zero codeBuddyCLIWire
	conversation, err := uuid.NewV7()
	if err != nil {
		return zero, fmt.Errorf("codebuddy: conversation id: %w", err)
	}
	request, err := uuid.NewV7()
	if err != nil {
		return zero, fmt.Errorf("codebuddy: request id: %w", err)
	}
	message, err := uuid.NewV7()
	if err != nil {
		return zero, fmt.Errorf("codebuddy: message id: %w", err)
	}
	trace, err := randomHex(16)
	if err != nil {
		return zero, err
	}
	span, err := randomHex(8)
	if err != nil {
		return zero, err
	}
	parent, err := randomHex(8)
	if err != nil {
		return zero, err
	}
	return codeBuddyCLIWire{
		codeBuddyChatIDs: codeBuddyChatIDs{
			ConversationID:        conversation.String(),
			ConversationRequestID: hex.EncodeToString(request[:]),
			MessageID:             hex.EncodeToString(message[:]),
			TraceID:               trace,
		},
		SpanID:       span,
		ParentSpanID: parent,
	}, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("codebuddy: trace id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// codeBuddyActivityFingerprint matches the captured CodeBuddy CLI 2.156.0
// Windows client. The patrol runs on Linux, but the gift is credited to a
// CLI-local conversation, so the report OS stays win32/x64.
func codeBuddyActivityFingerprint(userID, username, machineID, sessionID string) map[string]any {
	fp := codeBuddyCLIFingerprint(userID, username, machineID, sessionID)
	fp["os"] = "win32"
	fp["arch"] = "x64"
	fp["osVersion"] = "10.0.26200"
	fp["cpuModel"] = "AMD Ryzen 7 7840HS w/ Radeon 780M Graphics     "
	fp["cpuCores"] = 16
	fp["memorySize"] = 28
	return fp
}

func codeBuddyLifecycleFingerprint(fp map[string]any) map[string]any {
	out := make(map[string]any, len(fp))
	for key, value := range fp {
		if key == "agentName" || key == "agentType" {
			continue
		}
		out[key] = value
	}
	return out
}

func codeBuddyPluginStartEvent(fp map[string]any, now int64) map[string]any {
	return mergeCodeBuddyActivityEvent(codeBuddyLifecycleFingerprint(fp), map[string]any{
		"eventCode":   "plugin_status",
		"timestamp":   now,
		"reportDelay": codeBuddyActivityReportDelay,
		"status":      "info",
		"text":        "pluginStart",
		"isInterval":  false,
	})
}

func codeBuddyLoginEvent(fp map[string]any, now int64) map[string]any {
	return mergeCodeBuddyActivityEvent(codeBuddyLifecycleFingerprint(fp), map[string]any{
		"eventCode":   "user_auth_action",
		"timestamp":   now,
		"reportDelay": codeBuddyActivityReportDelay,
		"action":      "login",
		"actionAt":    now,
	})
}

func codeBuddyActivityUserParts(now time.Time) (string, []string) {
	if now.IsZero() {
		now = time.Now()
	}
	system := strings.ReplaceAll(codeBuddyCLISystemTemplate, "{{ACTIVITY_DATE}}", now.Format("Monday, Jan 2, 2006"))
	return system, []string{codeBuddyCLIMemoryText, codeBuddyCLIRulesText, codeBuddyCLIActivityQuery}
}

func codeBuddyActivityInputLength(parts []string) int {
	n := 0
	for _, part := range parts {
		n += utf8.RuneCountInString(part)
	}
	return n
}

func marshalJSONNoHTML(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func gzipBody(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	if err != nil {
		return nil, fmt.Errorf("codebuddy: gzip activity chat: %w", err)
	}
	if _, err = zw.Write(raw); err != nil {
		return nil, fmt.Errorf("codebuddy: gzip activity chat: %w", err)
	}
	if err = zw.Close(); err != nil {
		return nil, fmt.Errorf("codebuddy: gzip activity chat: %w", err)
	}
	return buf.Bytes(), nil
}

func applyCodeBuddyCLIActivityHeaders(req *http.Request, accessToken, userID, domain string, wire codeBuddyCLIWire) {
	if req == nil {
		return
	}
	NewCodeBuddyExecutor(nil).applyHeadersWithIDs(req, accessToken, userID, domain, wire.codeBuddyChatIDs)
	req.Header.Set("x-stainless-arch", "x64")
	req.Header.Set("x-stainless-lang", "js")
	req.Header.Set("x-stainless-os", "Windows")
	req.Header.Set("x-stainless-package-version", "6.25.0")
	req.Header.Set("x-stainless-retry-count", "0")
	req.Header.Set("x-stainless-runtime", "node")
	req.Header.Set("x-stainless-runtime-version", "v24.17.0")
	req.Header.Set("traceparent", fmt.Sprintf("00-%s-%s-01", wire.TraceID, wire.SpanID))
	req.Header.Set("b3", fmt.Sprintf("%s-%s-1-%s", wire.TraceID, wire.SpanID, wire.ParentSpanID))
	req.Header.Set("X-B3-TraceId", wire.TraceID)
	req.Header.Set("X-B3-ParentSpanId", wire.ParentSpanID)
	req.Header.Set("X-B3-SpanId", wire.SpanID)
	req.Header.Set("X-B3-Sampled", "1")
}
