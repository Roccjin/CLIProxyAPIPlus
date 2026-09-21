package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const (
	codeBuddyReportPath          = "/v2/report"
	codeBuddyActivityMaxTokens   = 128000
	codeBuddyActivityReportDelay = 2000
)

// CodeBuddyActivityPingResult is the outcome of one international CLI activity ping.
type CodeBuddyActivityPingResult struct {
	MachineID    string
	SessionID    string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	FinishReason string
}

// PingCodeBuddyDailyActivity sends the official CLI activity chain against
// www.codebuddy.ai: plugin start, login, /v2/report send, gzipped
// /v2/chat/completions, then the response reports. It never targets WorkBuddy
// or the CN gateway.
func PingCodeBuddyDailyActivity(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, model string) (*CodeBuddyActivityPingResult, error) {
	if auth == nil {
		return nil, fmt.Errorf("codebuddy: missing auth")
	}
	accessToken, _, domain := codeBuddyCredentials(auth)
	if accessToken == "" {
		return nil, fmt.Errorf("codebuddy: missing access token")
	}
	if !codebuddy.IsGlobalDomain(domain) {
		return nil, fmt.Errorf("codebuddy: activity ping is international-only")
	}
	return pingCodeBuddyDailyActivity(ctx, auth, cfg, codebuddy.APIBaseURLForDomain(domain), model)
}

func pingCodeBuddyDailyActivity(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, apiBase, model string) (*CodeBuddyActivityPingResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	accessToken, userID, domain := codeBuddyCredentials(auth)
	if accessToken == "" {
		return nil, fmt.Errorf("codebuddy: missing access token")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = config.DefaultBuddyActivityPatrolModel
	}
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if apiBase == "" {
		return nil, fmt.Errorf("codebuddy: missing activity API base")
	}

	model = resolveCodeBuddyActivityModel(model)
	machineID := ensureCodeBuddyCLIMachineID(auth)
	sessionID := uuid.NewString()
	wire, err := newCodeBuddyCLIWire()
	if err != nil {
		return nil, err
	}
	ids := wire.codeBuddyChatIDs
	fp := codeBuddyActivityFingerprint(userID, codeBuddyActivityUsername(auth), machineID, sessionID)
	now := time.Now().UnixMilli()

	if err = postCodeBuddyReport(ctx, auth, cfg, apiBase, accessToken, userID, domain, []map[string]any{
		codeBuddyPluginStartEvent(fp, now),
	}); err != nil {
		return nil, err
	}
	if err = postCodeBuddyReport(ctx, auth, cfg, apiBase, accessToken, userID, domain, []map[string]any{
		codeBuddyLoginEvent(fp, now+1),
	}); err != nil {
		return nil, err
	}

	payload, inputLength, err := buildCodeBuddyActivityChatPayload(model, domain)
	if err != nil {
		return nil, err
	}
	sendAt := time.Now().UnixMilli()
	sendEvents := []map[string]any{
		codeBuddyChatRequestSendEvent(fp, ids, model, sendAt, inputLength),
		codeBuddyChatMessageSendEvent(fp, ids, model, sendAt+1),
	}
	if err = postCodeBuddyReport(ctx, auth, cfg, apiBase, accessToken, userID, domain, sendEvents); err != nil {
		return nil, err
	}

	usage, finishReason, err := postCodeBuddyActivityChat(ctx, auth, cfg, apiBase, accessToken, userID, domain, wire, payload)
	if err != nil {
		return nil, err
	}
	doneAt := time.Now().UnixMilli()
	if err = postCodeBuddyReport(ctx, auth, cfg, apiBase, accessToken, userID, domain, []map[string]any{
		codeBuddyChatMessageResponseEvent(fp, ids, model, doneAt, usage, finishReason, true),
		codeBuddyChatMessageStatusEvent(fp, ids, model, doneAt+1),
	}); err != nil {
		return nil, err
	}
	if err = postCodeBuddyReport(ctx, auth, cfg, apiBase, accessToken, userID, domain, []map[string]any{
		codeBuddyChatRequestResponseEvent(fp, ids, model, doneAt+2, usage, finishReason, true),
	}); err != nil {
		return nil, err
	}
	return &CodeBuddyActivityPingResult{
		MachineID:    machineID,
		SessionID:    sessionID,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		FinishReason: finishReason,
	}, nil
}

func ensureCodeBuddyCLIMachineID(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return uuid.NewString()
	}
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	existing := metaStringValue(auth.Metadata, cliproxyauth.MetadataKeyCLIMachineID)
	if existing != "" {
		return existing
	}
	id := uuid.NewString()
	auth.Metadata[cliproxyauth.MetadataKeyCLIMachineID] = id
	return id
}

func codeBuddyActivityUsername(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, key := range []string{"email", "username", "nickname", "preferred_username", "user_name"} {
		if v := metaStringValue(auth.Metadata, key); v != "" {
			return v
		}
	}
	return metaStringValue(auth.Metadata, "user_id")
}

func codeBuddyCLIRuntime() (osName, arch, osVersion string) {
	switch runtime.GOOS {
	case "windows":
		osName, osVersion = "win32", "10.0.26200"
	case "darwin":
		osName, osVersion = "darwin", "24.0.0"
	default:
		osName, osVersion = "linux", "6.8.0"
	}
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "ia32"
	default:
		arch = runtime.GOARCH
	}
	return osName, arch, osVersion
}

func codeBuddyCLIFingerprint(userID, username, machineID, sessionID string) map[string]any {
	osName, arch, osVersion := codeBuddyCLIRuntime()
	fp := map[string]any{
		"timezone":      "Asia/Shanghai",
		"userId":        userID,
		"product":       "SaaS",
		"releaseDate":   codebuddy.CLIReleaseDateMS,
		"commit":        codebuddy.CLICommit,
		"os":            osName,
		"arch":          arch,
		"osVersion":     osVersion,
		"cpuModel":      "generic",
		"cpuCores":      runtime.NumCPU(),
		"memorySize":    16,
		"extName":       codebuddy.CLIExtName,
		"extVersion":    codebuddy.ClientVersion,
		"ideName":       "CLI",
		"ideType":       "CLI",
		"machineId":     machineID,
		"sessionId":     sessionID,
		"ideVersion":    codebuddy.ClientVersion,
		"featureModule": "cli_local",
		"agentName":     "cli",
		"agentType":     "main",
	}
	if username != "" {
		fp["username"] = username
		fp["userNickname"] = username
	}
	return fp
}

func mergeCodeBuddyActivityEvent(fp map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(fp)+len(extra))
	for k, v := range fp {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func codeBuddyModelDisplayName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return model
	}
	parts := strings.Split(model, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "-")
}

func codeBuddyChatRequestSendEvent(fp map[string]any, ids codeBuddyChatIDs, model string, now int64, inputLength int) map[string]any {
	return mergeCodeBuddyActivityEvent(fp, map[string]any{
		"eventCode":             "chat_request_send",
		"timestamp":             now,
		"reportDelay":           codeBuddyActivityReportDelay,
		"mode":                  "unknown",
		"conversationId":        ids.ConversationID,
		"requestId":             ids.ConversationRequestID,
		"inputLength":           inputLength,
		"requestModelId":        model,
		"requestModelName":      codeBuddyModelDisplayName(model),
		"isPlan":                false,
		"isAutoExecuteTerminal": false,
		"isAutoModify":          false,
		"codebaseEnable":        false,
		"maxToken":              0,
		"maxSteps":              500,
		"temperature":           0,
		"maxRetries":            0,
		"mentionContexts":       []any{},
		"knowledgeId":           []any{},
		"knowledgeName":         []any{},
		"codebaseId":            "",
		"mentionContextCount":   0,
		"command":               "",
		"recommendId":           "",
		"skillId":               "",
		"skillCount":            0,
		"totalCount":            0,
		"vcsType":               "unknown",
		"vcsRepo":               "",
		"vcsBranchName":         "",
		"vcsRevId":              "",
		"presentAt":             now,
		"traceId":               ids.TraceID,
		"rootRequestId":         ids.ConversationRequestID,
		"parentConversationId":  ids.ConversationID,
	})
}

func codeBuddyChatMessageSendEvent(fp map[string]any, ids codeBuddyChatIDs, model string, now int64) map[string]any {
	return mergeCodeBuddyActivityEvent(fp, map[string]any{
		"eventCode":            "chat_message_send",
		"timestamp":            now,
		"reportDelay":          codeBuddyActivityReportDelay,
		"conversationId":       ids.ConversationID,
		"requestId":            ids.ConversationRequestID,
		"messageId":            ids.MessageID,
		"requestModelId":       model,
		"requestModelName":     codeBuddyModelDisplayName(model),
		"historyCount":         1,
		"isContextTruncated":   false,
		"currentStepCount":     1,
		"presentAt":            now,
		"traceId":              ids.TraceID,
		"rootRequestId":        ids.ConversationRequestID,
		"parentConversationId": ids.ConversationID,
	})
}

func codeBuddyChatMessageResponseEvent(fp map[string]any, ids codeBuddyChatIDs, model string, now int64, usage codeBuddyActivityUsage, finishReason string, ok bool) map[string]any {
	if finishReason == "" {
		finishReason = "stop"
	}
	return mergeCodeBuddyActivityEvent(fp, map[string]any{
		"eventCode":            "chat_message_response",
		"timestamp":            now,
		"reportDelay":          codeBuddyActivityReportDelay,
		"conversationId":       ids.ConversationID,
		"requestId":            ids.ConversationRequestID,
		"messageId":            ids.MessageID,
		"requestModelId":       model,
		"requestModelName":     codeBuddyModelDisplayName(model),
		"responseModelId":      model,
		"inputToken":           usage.InputTokens,
		"outputToken":          usage.OutputTokens,
		"totalToken":           usage.TotalTokens,
		"cachedTokens":         usage.CachedTokens,
		"cachedWriteTokens":    usage.CacheCreationTokens,
		"cachedMissTokens":     0,
		"isSuccessful":         ok,
		"messageErrorCode":     "",
		"finishReason":         finishReason,
		"firstTokenAt":         now,
		"presentAt":            now,
		"traceId":              ids.TraceID,
		"rootRequestId":        ids.ConversationRequestID,
		"parentConversationId": ids.ConversationID,
	})
}

func codeBuddyChatMessageStatusEvent(fp map[string]any, ids codeBuddyChatIDs, model string, now int64) map[string]any {
	return mergeCodeBuddyActivityEvent(fp, map[string]any{
		"eventCode":            "chat_message_status",
		"timestamp":            now,
		"reportDelay":          codeBuddyActivityReportDelay,
		"conversationId":       ids.ConversationID,
		"requestId":            ids.ConversationRequestID,
		"messageId":            ids.MessageID,
		"requestModelId":       model,
		"requestModelName":     codeBuddyModelDisplayName(model),
		"messageErrorCode":     "0",
		"traceId":              ids.TraceID,
		"rootRequestId":        ids.ConversationRequestID,
		"parentConversationId": ids.ConversationID,
	})
}

func codeBuddyChatRequestResponseEvent(fp map[string]any, ids codeBuddyChatIDs, model string, now int64, usage codeBuddyActivityUsage, finishReason string, ok bool) map[string]any {
	if finishReason == "" {
		finishReason = "stop"
	}
	return mergeCodeBuddyActivityEvent(fp, map[string]any{
		"eventCode":            "chat_request_response",
		"timestamp":            now,
		"reportDelay":          codeBuddyActivityReportDelay,
		"mode":                 "unknown",
		"conversationId":       ids.ConversationID,
		"requestId":            ids.ConversationRequestID,
		"requestModelId":       model,
		"requestModelName":     codeBuddyModelDisplayName(model),
		"toolCallCount":        0,
		"inputToken":           usage.InputTokens,
		"outputToken":          usage.OutputTokens,
		"totalToken":           usage.TotalTokens,
		"cachedTokens":         usage.CachedTokens,
		"cachedWriteTokens":    usage.CacheCreationTokens,
		"cachedMissTokens":     0,
		"isSuccessful":         ok,
		"messageErrorCode":     "",
		"finishReason":         finishReason,
		"presentAt":            now,
		"rootRequestId":        ids.ConversationRequestID,
		"parentConversationId": ids.ConversationID,
	})
}

type codeBuddyActivityUsage struct {
	InputTokens         int64
	OutputTokens        int64
	TotalTokens         int64
	CachedTokens        int64
	CacheCreationTokens int64
}

func applyCodeBuddyReportHeaders(req *http.Request, accessToken, userID, domain, requestID string) {
	if requestID == "" {
		requestID = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("User-Agent", codebuddy.UserAgent)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Domain", domain)
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
}

func postCodeBuddyReport(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, apiBase, accessToken, userID, domain string, events []map[string]any) error {
	raw, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("codebuddy: encode activity report: %w", err)
	}
	url := apiBase + codeBuddyReportPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("codebuddy: build activity report: %w", err)
	}
	applyCodeBuddyReportHeaders(req, accessToken, userID, domain, strings.ReplaceAll(uuid.NewString(), "-", ""))

	resp, err := newProxyAwareHTTPClient(ctx, cfg, auth, 0).Do(req)
	if err != nil {
		return fmt.Errorf("codebuddy: activity report request failed: %w", err)
	}
	body, errRead := io.ReadAll(resp.Body)
	if errClose := resp.Body.Close(); errClose != nil {
		log.Errorf("codebuddy: close activity report body error: %v", errClose)
	}
	if errRead != nil {
		return fmt.Errorf("codebuddy: read activity report: %w", errRead)
	}
	if !isHTTPSuccess(resp.StatusCode) {
		failure := helps.ClassifyBuddyFailure(resp.StatusCode, body)
		if normalized := helps.NewBuddyFailureError("codebuddy", failure); normalized != nil {
			return fmt.Errorf("codebuddy: activity report status %d: %w", resp.StatusCode, normalized)
		}
		return fmt.Errorf("codebuddy: activity report status %d: %s", resp.StatusCode, truncateActivityError(body))
	}
	code := gjson.GetBytes(body, "code")
	if code.Exists() && code.Int() != 0 {
		return fmt.Errorf("codebuddy: activity report code %d: %s", code.Int(), gjson.GetBytes(body, "msg").String())
	}
	return nil
}

func postCodeBuddyActivityChat(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, apiBase, accessToken, userID, domain string, wire codeBuddyCLIWire, payload []byte) (codeBuddyActivityUsage, string, error) {
	var empty codeBuddyActivityUsage
	gzipped, err := gzipBody(payload)
	if err != nil {
		return empty, "", err
	}
	url := apiBase + codeBuddyChatPath
	httpClient := newProxyAwareHTTPClient(ctx, cfg, auth, 0)

	var lastErr error
	for attempt := 0; attempt <= codeBuddyTransientProviderRetries; attempt++ {
		httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(gzipped))
		if errReq != nil {
			return empty, "", errReq
		}
		applyCodeBuddyCLIActivityHeaders(httpReq, accessToken, userID, domain, wire)
		httpReq.Header.Set("Content-Encoding", "gzip")

		httpResp, errDo := httpClient.Do(httpReq)
		if errDo != nil {
			lastErr = fmt.Errorf("codebuddy: activity chat request failed: %w", errDo)
			if attempt < codeBuddyTransientProviderRetries && isCodeBuddyTransientNetworkError(errDo) {
				continue
			}
			return empty, "", lastErr
		}
		body, errRead := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("codebuddy: close activity chat body error: %v", errClose)
		}
		if errRead != nil {
			return empty, "", fmt.Errorf("codebuddy: read activity chat: %w", errRead)
		}
		if isHTTPSuccess(httpResp.StatusCode) {
			_, usageDetail, aggErr := aggregateOpenAIChatCompletionStream(body)
			if aggErr != nil {
				return empty, "", fmt.Errorf("codebuddy: decode activity chat: %w", aggErr)
			}
			finish := gjson.GetBytes(body, "choices.0.finish_reason").String()
			if finish == "" {
				// Stream chunks store finish_reason on later SSE lines; scan them.
				for _, line := range bytes.Split(body, []byte("\n")) {
					line = bytes.TrimSpace(line)
					if !bytes.HasPrefix(line, []byte("data:")) {
						continue
					}
					payload := bytes.TrimSpace(line[5:])
					if reason := gjson.GetBytes(payload, "choices.0.finish_reason").String(); reason != "" {
						finish = reason
					}
				}
			}
			return codeBuddyActivityUsage{
				InputTokens:         usageDetail.InputTokens,
				OutputTokens:        usageDetail.OutputTokens,
				TotalTokens:         usageDetail.TotalTokens,
				CachedTokens:        usageDetail.CachedTokens,
				CacheCreationTokens: usageDetail.CacheCreationTokens,
			}, finish, nil
		}

		failure := helps.ClassifyBuddyFailure(httpResp.StatusCode, body)
		lastErr = fmt.Errorf("codebuddy: activity chat status %d: %s", httpResp.StatusCode, truncateActivityError(body))
		if normalized := helps.NewBuddyFailureError("codebuddy", failure); normalized != nil {
			lastErr = fmt.Errorf("codebuddy: activity chat status %d: %w", httpResp.StatusCode, normalized)
		}
		if attempt < codeBuddyTransientProviderRetries && (failure.Kind == helps.BuddyFailureGatewayTimeout || failure.Kind == helps.BuddyFailureTransient) {
			continue
		}
		return empty, "", lastErr
	}
	return empty, "", lastErr
}

func buildCodeBuddyActivityChatPayload(model, domain string) ([]byte, int, error) {
	model = resolveCodeBuddyActivityModel(model)
	system, parts := codeBuddyActivityUserParts(time.Now())
	content := make([]map[string]string, 0, len(parts))
	for _, part := range parts {
		content = append(content, map[string]string{"type": "text", "text": part})
	}
	messages, err := marshalJSONNoHTML([]any{
		map[string]any{"role": "system", "content": system},
		map[string]any{"role": "user", "content": content},
	})
	if err != nil {
		return nil, 0, err
	}
	body, err := marshalJSONNoHTML(struct {
		Model            string          `json:"model"`
		Messages         json.RawMessage `json:"messages"`
		Tools            json.RawMessage `json:"tools"`
		Temperature      int             `json:"temperature"`
		MaxTokens        int             `json:"max_tokens"`
		Stream           bool            `json:"stream"`
		StreamOptions    map[string]any  `json:"stream_options"`
		ReasoningEffort  string          `json:"reasoning_effort"`
		Verbosity        string          `json:"verbosity"`
		ReasoningSummary string          `json:"reasoning_summary"`
	}{
		Model:            model,
		Messages:         messages,
		Tools:            json.RawMessage(codeBuddyCLIToolsJSON),
		Temperature:      1,
		MaxTokens:        codeBuddyActivityMaxTokens,
		Stream:           true,
		StreamOptions:    map[string]any{"include_usage": true},
		ReasoningEffort:  codeBuddyCLIReasoningEffort,
		Verbosity:        codeBuddyCLIVerbosity,
		ReasoningSummary: codeBuddyDefaultReasoningSummary,
	})
	if err != nil {
		return nil, 0, err
	}
	return prepareCodeBuddyChatPayload(body, domain), codeBuddyActivityInputLength(parts), nil
}

func truncateActivityError(body []byte) string {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 240 {
		return msg[:240]
	}
	return msg
}
