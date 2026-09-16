package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	workBuddyChatPath                 = "/v2/chat/completions"
	workBuddyAuthType                 = "workbuddy"
	workBuddyDefaultSystemPrompt      = "You are a helpful assistant."
	workBuddyTransientProviderRetries = 3
	workBuddyQuotaTransientRetries    = 1
	workBuddyTransientRetryBaseDelay  = 400 * time.Millisecond
	workBuddyTransientRetryMaxDelay   = 2 * time.Second
	workBuddyQuotaRetryAfterMax       = 30 * time.Second
)

// Official CLI chat bodies only send these fields. Open WebUI and similar
// clients attach session/chat metadata that www.workbuddy.ai rejects with
// 400/11128 ("Illegal API invocation from an unapproved channel").
var workBuddyChatAllowedFields = []string{
	"model",
	"messages",
	"stream",
	"stream_options",
	"temperature",
	"top_p",
	"max_tokens",
	"max_completion_tokens",
	"stop",
	"n",
	"user",
	"tools",
	"tool_choice",
	"parallel_tool_calls",
	"functions",
	"function_call",
	"response_format",
	"reasoning_effort",
	"reasoning_summary",
	"verbosity",
	"thinking",
	"presence_penalty",
	"frequency_penalty",
}

// WorkBuddyExecutor handles requests to the WorkBuddy API.
type WorkBuddyExecutor struct {
	cfg *config.Config
}

// NewWorkBuddyExecutor creates a new WorkBuddy executor instance.
func NewWorkBuddyExecutor(cfg *config.Config) *WorkBuddyExecutor {
	return &WorkBuddyExecutor{cfg: cfg}
}

// Identifier returns the unique identifier for this executor.
func (e *WorkBuddyExecutor) Identifier() string { return workBuddyAuthType }

// workBuddyCredentials extracts the access token and domain from auth metadata.
func workBuddyCredentials(auth *cliproxyauth.Auth) (accessToken, userID, domain string) {
	if auth == nil {
		return "", "", ""
	}
	accessToken = metaStringValue(auth.Metadata, "access_token")
	userID = metaStringValue(auth.Metadata, "user_id")
	domain = metaStringValue(auth.Metadata, "domain")
	if domain == "" {
		domain = workbuddy.DefaultDomain
	}
	return
}

// PrepareRequest prepares the HTTP request before execution.
func (e *WorkBuddyExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	accessToken, userID, domain := workBuddyCredentials(auth)
	if accessToken == "" {
		return fmt.Errorf("workbuddy: missing access token")
	}
	e.applyHeaders(req, accessToken, userID, domain)
	rewriteWorkBuddyRequestURL(req, domain)
	return nil
}

// HttpRequest executes a raw HTTP request.
func (e *WorkBuddyExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("workbuddy executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if err := e.PrepareRequest(httpReq, auth); err != nil {
		return nil, err
	}
	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

// Execute performs a non-streaming request.
func (e *WorkBuddyExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := newUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.trackFailure(ctx, &err)

	accessToken, userID, domain := workBuddyCredentials(auth)
	if accessToken == "" {
		return resp, fmt.Errorf("workbuddy: missing access token")
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")

	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalTranslated := sdktranslator.TranslateRequest(from, to, baseModel, originalPayloadSource, true)
	translated := sdktranslator.TranslateRequest(from, to, baseModel, req.Payload, true)
	requestedModel := payloadRequestedModel(opts, req.Model)
	translated = applyPayloadConfigWithRoot(e.cfg, baseModel, to.String(), "", translated, originalTranslated, requestedModel)

	modelInfo := lookupWorkBuddyModelInfo(baseModel)
	translated, err = thinking.ApplyThinkingWithModelInfo(translated, translated, req.Model, from.String(), to.String(), e.Identifier(), modelInfo)
	if err != nil {
		return resp, err
	}
	translated = prepareWorkBuddyChatPayloadFrom(translated, originalPayloadSource, domain, modelInfo)

	httpResp, err := e.doWorkBuddyChat(ctx, auth, accessToken, userID, domain, translated, resolveWorkBuddyConversationID(opts, req.Payload))
	if err != nil {
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("workbuddy executor: close response body error: %v", errClose)
		}
	}()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	appendAPIResponseChunk(ctx, e.cfg, body)
	aggregatedBody, usageDetail, err := aggregateOpenAIChatCompletionStream(body)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	reporter.publish(ctx, usageDetail)
	reporter.ensurePublished(ctx)

	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, opts.OriginalRequest, translated, aggregatedBody, &param)
	resp = cliproxyexecutor.Response{Payload: []byte(out), Headers: httpResp.Header.Clone()}
	return resp, nil
}

// ExecuteStream performs a streaming request.
func (e *WorkBuddyExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := newUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.trackFailure(ctx, &err)

	accessToken, userID, domain := workBuddyCredentials(auth)
	if accessToken == "" {
		return nil, fmt.Errorf("workbuddy: missing access token")
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")

	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalTranslated := sdktranslator.TranslateRequest(from, to, baseModel, originalPayloadSource, true)
	translated := sdktranslator.TranslateRequest(from, to, baseModel, req.Payload, true)
	requestedModel := payloadRequestedModel(opts, req.Model)
	translated = applyPayloadConfigWithRoot(e.cfg, baseModel, to.String(), "", translated, originalTranslated, requestedModel)

	modelInfo := lookupWorkBuddyModelInfo(baseModel)
	translated, err = thinking.ApplyThinkingWithModelInfo(translated, translated, req.Model, from.String(), to.String(), e.Identifier(), modelInfo)
	if err != nil {
		return nil, err
	}
	translated = prepareWorkBuddyChatPayloadFrom(translated, originalPayloadSource, domain, modelInfo)

	httpResp, err := e.doWorkBuddyChat(ctx, auth, accessToken, userID, domain, translated, resolveWorkBuddyConversationID(opts, req.Payload))
	if err != nil {
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("workbuddy executor: close stream body error: %v", errClose)
			}
		}()

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, maxScannerBufferSize)
		var param any
		for scanner.Scan() {
			line := scanner.Bytes()
			appendAPIResponseChunk(ctx, e.cfg, line)
			if detail, ok := parseOpenAIStreamUsage(line); ok {
				reporter.publish(ctx, detail)
			}
			if len(line) == 0 {
				continue
			}
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			normalized := normalizeWorkBuddyChatStreamLine(bytes.Clone(line))
			chunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, opts.OriginalRequest, translated, normalized, &param)
			for i := range chunks {
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunks[i])}
			}
		}
		if errScan := scanner.Err(); errScan != nil {
			recordAPIResponseError(ctx, e.cfg, errScan)
			reporter.publishFailure(ctx)
			out <- cliproxyexecutor.StreamChunk{Err: errScan}
		}
		reporter.ensurePublished(ctx)
	}()

	return &cliproxyexecutor.StreamResult{
		Headers: httpResp.Header.Clone(),
		Chunks:  out,
	}, nil
}

// Refresh exchanges the WorkBuddy refresh token for a new access token.
func (e *WorkBuddyExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	if auth == nil {
		return nil, fmt.Errorf("workbuddy: missing auth")
	}

	refreshToken := metaStringValue(auth.Metadata, "refresh_token")
	if refreshToken == "" {
		log.Debugf("workbuddy executor: no refresh token available, skipping refresh")
		return auth, nil
	}

	accessToken, userID, domain := workBuddyCredentials(auth)

	authSvc := workbuddy.NewWorkBuddyAuthWithProxyURL(e.cfg, auth.ProxyURL)
	storage, err := authSvc.RefreshToken(ctx, accessToken, refreshToken, userID, domain)
	if err != nil {
		return nil, fmt.Errorf("workbuddy: token refresh failed: %w", err)
	}

	updated := auth.Clone()
	applyWorkBuddyRefreshedTokens(updated, storage)
	now := time.Now()
	updated.UpdatedAt = now
	updated.LastRefreshedAt = now

	return updated, nil
}

func applyWorkBuddyRefreshedTokens(auth *cliproxyauth.Auth, storage *workbuddy.WorkBuddyTokenStorage) {
	if auth == nil || storage == nil {
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Provider = workBuddyAuthType
	auth.Metadata["type"] = workBuddyAuthType
	auth.Metadata["access_token"] = storage.AccessToken
	if storage.RefreshToken != "" {
		auth.Metadata["refresh_token"] = storage.RefreshToken
	}
	auth.Metadata["expires_in"] = storage.ExpiresIn
	if storage.RefreshExpiresIn != 0 {
		auth.Metadata["refresh_expires_in"] = storage.RefreshExpiresIn
	}
	if storage.Domain != "" {
		auth.Metadata["domain"] = storage.Domain
	}
	if storage.UserID != "" {
		auth.Metadata["user_id"] = storage.UserID
	}
	if storage.TokenType != "" {
		auth.Metadata["token_type"] = storage.TokenType
	}
	next := *storage
	if strings.TrimSpace(next.Type) == "" {
		next.Type = workBuddyAuthType
	}
	auth.Storage = &next
}

func adoptWorkBuddyRefreshedAuth(dst, src *cliproxyauth.Auth) {
	if dst == nil || src == nil || dst == src {
		return
	}
	dst.Metadata = src.Metadata
	dst.Storage = src.Storage
	dst.Provider = src.Provider
	dst.UpdatedAt = src.UpdatedAt
	dst.LastRefreshedAt = src.LastRefreshedAt
}

// CountTokens is not supported for WorkBuddy.
func (e *WorkBuddyExecutor) CountTokens(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, fmt.Errorf("workbuddy: count tokens not supported")
}

func workBuddyChatURL(domain string) string {
	return workbuddy.APIBaseURLForDomain(domain) + workBuddyChatPath
}

func (e *WorkBuddyExecutor) doWorkBuddyChat(ctx context.Context, auth *cliproxyauth.Auth, accessToken, userID, domain string, body []byte, conversationID string) (*http.Response, error) {
	url := workBuddyChatURL(domain)
	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}

	var lastErr error
	for attempt := 0; attempt <= workBuddyTransientProviderRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		e.applyWorkBuddyHeaders(httpReq, accessToken, userID, domain, conversationID)
		httpReq.Header.Set("Cache-Control", "no-cache")
		if attempt == 0 {
			recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
				URL:       url,
				Method:    http.MethodPost,
				Headers:   httpReq.Header.Clone(),
				Body:      body,
				Provider:  e.Identifier(),
				AuthID:    authID,
				AuthLabel: authLabel,
				AuthType:  authType,
				AuthValue: authValue,
			})
		}

		httpResp, err := httpClient.Do(httpReq)
		if err != nil {
			recordAPIResponseError(ctx, e.cfg, err)
			lastErr = err
			if attempt < workBuddyTransientProviderRetries && isWorkBuddyTransientNetworkError(err) {
				delay := workBuddyTransientRetryBackoff(attempt)
				log.Warnf("workbuddy executor: transient network error: %v; retrying in %s", err, delay)
				if errWait := workBuddyRetryWait(ctx, delay); errWait != nil {
					return nil, errWait
				}
				continue
			}
			return nil, err
		}
		recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
		if isHTTPSuccess(httpResp.StatusCode) {
			return httpResp, nil
		}

		errBody, _ := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("workbuddy executor: close error response body: %v", errClose)
		}
		appendAPIResponseChunk(ctx, e.cfg, errBody)
		summary := summarizeErrorBody(httpResp.Header.Get("Content-Type"), errBody)
		failure := helps.ClassifyBuddyFailure(httpResp.StatusCode, errBody)
		lastErr = statusErr{code: httpResp.StatusCode, msg: string(errBody)}
		if normalizedErr := helps.NewBuddyFailureError(e.Identifier(), failure); normalizedErr != nil {
			lastErr = normalizedErr
		}
		if isWorkBuddyAccountRiskControl(httpResp.StatusCode, errBody) {
			log.Warnf("workbuddy executor: account risk control status: %d, body: %s", httpResp.StatusCode, summary)
			return nil, lastErr
		}
		retryable := failure.Kind == helps.BuddyFailureGatewayTimeout ||
			failure.Kind == helps.BuddyFailureTransient ||
			isWorkBuddyTransientProviderError(httpResp.StatusCode, errBody)
		if attempt < workBuddyTransientProviderRetries && retryable {
			delay := workBuddyTransientRetryBackoff(attempt)
			log.Warnf("workbuddy executor: transient upstream error status: %d, body: %s; retrying in %s", httpResp.StatusCode, summary, delay)
			if errWait := workBuddyRetryWait(ctx, delay); errWait != nil {
				return nil, errWait
			}
			continue
		}
		log.Warnf("workbuddy executor: upstream error status: %d, body: %s", httpResp.StatusCode, summary)
		return nil, lastErr
	}
	return nil, lastErr
}

func workBuddyTransientRetryBackoff(attempt int) time.Duration {
	if attempt < 0 {
		return workBuddyTransientRetryBaseDelay
	}
	delay := workBuddyTransientRetryBaseDelay
	for i := 0; i < attempt; i++ {
		if delay >= workBuddyTransientRetryMaxDelay {
			return workBuddyTransientRetryMaxDelay
		}
		delay *= 2
	}
	if delay > workBuddyTransientRetryMaxDelay {
		return workBuddyTransientRetryMaxDelay
	}
	return delay
}

var workBuddyRetryWait = waitWorkBuddyRetry

func waitWorkBuddyRetry(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isWorkBuddyAccountRiskControl(status int, body []byte) bool {
	switch status {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusInternalServerError:
	default:
		return false
	}
	blob := strings.ToLower(strings.Join([]string{
		gjson.GetBytes(body, "code").String(),
		gjson.GetBytes(body, "msg").String(),
		gjson.GetBytes(body, "message").String(),
		gjson.GetBytes(body, "data.details").String(),
		gjson.GetBytes(body, "data.code").String(),
		gjson.GetBytes(body, "error.data.details").String(),
		gjson.GetBytes(body, "error.data.code").String(),
		gjson.GetBytes(body, "error.message").String(),
		string(body),
	}, " "))
	return strings.Contains(blob, "request illegal") || strings.Contains(blob, "11140")
}

func isWorkBuddyTransientProviderError(status int, body []byte) bool {
	return helps.ClassifyBuddyFailure(status, body).Kind == helps.BuddyFailureTransient
}

func isWorkBuddyTransientNetworkError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tls handshake timeout") ||
		strings.Contains(msg, "timeout awaiting response headers") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "unexpected eof")
}

// normalizeWorkBuddyChatStreamLine rewrites WorkBuddy international SSE chunks.
// www.workbuddy.ai /v2/chat/completions streams chat-style choices/delta payloads
// with object="response". The OpenAI Responses translator only accepts
// object="chat.completion.chunk", so unnormalized streams look empty and /v1/responses
// fails after the upstream 200.
func normalizeWorkBuddyChatStreamLine(line []byte) []byte {
	dataIdx := bytes.Index(line, []byte("data:"))
	if dataIdx < 0 {
		return line
	}
	jsonStart := dataIdx + len("data:")
	for jsonStart < len(line) && (line[jsonStart] == ' ' || line[jsonStart] == '\t') {
		jsonStart++
	}
	payload := bytes.TrimSpace(line[jsonStart:])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return line
	}

	updated := payload
	changed := false
	if obj := gjson.GetBytes(updated, "object"); obj.Exists() {
		switch strings.TrimSpace(obj.String()) {
		case "response", "chat.completion":
			next, err := sjson.SetBytes(updated, "object", "chat.completion.chunk")
			if err == nil {
				updated = next
				changed = true
			}
		}
	}

	choiceCount := int(gjson.GetBytes(updated, "choices.#").Int())
	for i := 0; i < choiceCount; i++ {
		prefix := fmt.Sprintf("choices.%d", i)
		if tcs := gjson.GetBytes(updated, prefix+".delta.tool_calls"); tcs.Exists() && tcs.IsArray() && len(tcs.Array()) == 0 {
			if next, err := sjson.DeleteBytes(updated, prefix+".delta.tool_calls"); err == nil {
				updated = next
				changed = true
			}
		}
		if extra := gjson.GetBytes(updated, prefix+".delta.extra_fields"); extra.Exists() {
			if next, err := sjson.DeleteBytes(updated, prefix+".delta.extra_fields"); err == nil {
				updated = next
				changed = true
			}
		}
		if fc := gjson.GetBytes(updated, prefix+".delta.function_call"); fc.Exists() && fc.Type == gjson.Null {
			if next, err := sjson.DeleteBytes(updated, prefix+".delta.function_call"); err == nil {
				updated = next
				changed = true
			}
		}
		if fr := gjson.GetBytes(updated, prefix+".finish_reason"); fr.Exists() && fr.Type == gjson.String && strings.TrimSpace(fr.String()) == "" {
			if next, err := sjson.SetBytes(updated, prefix+".finish_reason", nil); err == nil {
				updated = next
				changed = true
			}
		}
	}

	if !changed {
		return line
	}
	out := make([]byte, 0, jsonStart+len(updated))
	out = append(out, line[:jsonStart]...)
	out = append(out, updated...)
	return out
}

// prepareWorkBuddyChatPayload matches the international CLI chat body:
// stream + include_usage, optional reasoning_effort, and a leading system turn.
// Official chat bodies omit reasoning_summary unless the client sends it.
func prepareWorkBuddyChatPayload(payload []byte, domain string, modelInfo *registry.ModelInfo) []byte {
	return prepareWorkBuddyChatPayloadFrom(payload, nil, domain, modelInfo)
}

func prepareWorkBuddyChatPayloadFrom(payload, original []byte, domain string, modelInfo *registry.ModelInfo) []byte {
	if len(payload) == 0 {
		return payload
	}
	payload, _ = sjson.SetBytes(payload, "stream", true)
	payload, _ = sjson.SetBytes(payload, "stream_options.include_usage", true)
	payload = applyWorkBuddyReasoningEffort(payload, modelInfo)
	payload = restoreWorkBuddyDeveloperAsSystem(payload, original)
	payload = rewriteWorkBuddyDeveloperRoles(payload)
	payload = ensureWorkBuddySystemMessage(payload, domain)
	payload = remapWorkBuddyMaxTokens(payload)
	return sanitizeWorkBuddyChatPayload(payload)
}

// remapWorkBuddyMaxTokens prefers max_completion_tokens. OpenAI Responses
// translation copies max_output_tokens onto max_tokens; WorkBuddy forwards
// that field to the model provider, which rejects it as model_param_invalid
// (400/11133). Chat Completions clients already send max_completion_tokens
// and succeed with the same value.
func remapWorkBuddyMaxTokens(payload []byte) []byte {
	maxTokens := gjson.GetBytes(payload, "max_tokens")
	if !maxTokens.Exists() {
		return payload
	}
	if !gjson.GetBytes(payload, "max_completion_tokens").Exists() {
		payload, _ = sjson.SetRawBytes(payload, "max_completion_tokens", []byte(maxTokens.Raw))
	}
	payload, _ = sjson.DeleteBytes(payload, "max_tokens")
	return payload
}

// restoreWorkBuddyDeveloperAsSystem undoes openai-response → chat translation
// of role=developer to role=user. Chat Completions keeps developer until
// rewriteWorkBuddyDeveloperRoles maps it to system; Responses would otherwise
// prepend a dummy system turn and send the real instructions as a user message.
func restoreWorkBuddyDeveloperAsSystem(payload, original []byte) []byte {
	if !originalStartsWithDeveloper(original) {
		return payload
	}
	messages := gjson.GetBytes(payload, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return payload
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return payload
	}
	role := strings.ToLower(strings.TrimSpace(arr[0].Get("role").String()))
	if role != "user" && role != "developer" {
		return payload
	}
	out, err := sjson.SetBytes(payload, "messages.0.role", "system")
	if err != nil {
		return payload
	}
	return out
}

func originalStartsWithDeveloper(original []byte) bool {
	if len(original) == 0 {
		return false
	}
	root := gjson.ParseBytes(original)
	if strings.EqualFold(strings.TrimSpace(root.Get("messages.0.role").String()), "developer") {
		return true
	}
	input := root.Get("input")
	if !input.Exists() || !input.IsArray() {
		return false
	}
	arr := input.Array()
	if len(arr) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(arr[0].Get("role").String()), "developer")
}

// rewriteWorkBuddyDeveloperRoles maps OpenAI's developer role to system.
// GPT-5 / pi / OpenAI JS clients send role=developer. www.workbuddy.ai
// returns 400/11128 ("Illegal API invocation from an unapproved channel")
// when any message still has that role, even after a system turn is prepended.
func rewriteWorkBuddyDeveloperRoles(payload []byte) []byte {
	messages := gjson.GetBytes(payload, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return payload
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return payload
	}

	changed := false
	var b strings.Builder
	b.Grow(len(messages.Raw))
	b.WriteByte('[')
	for i, msg := range arr {
		if i > 0 {
			b.WriteByte(',')
		}
		if strings.EqualFold(strings.TrimSpace(msg.Get("role").String()), "developer") {
			next, err := sjson.Set(msg.Raw, "role", "system")
			if err != nil {
				b.WriteString(msg.Raw)
				continue
			}
			b.WriteString(next)
			changed = true
			continue
		}
		b.WriteString(msg.Raw)
	}
	b.WriteByte(']')
	if !changed {
		return payload
	}
	out, err := sjson.SetRawBytes(payload, "messages", []byte(b.String()))
	if err != nil {
		return payload
	}
	return out
}

func sanitizeWorkBuddyChatPayload(payload []byte) []byte {
	root := gjson.ParseBytes(payload)
	if !root.IsObject() {
		return payload
	}
	out := []byte(`{}`)
	for _, key := range workBuddyChatAllowedFields {
		value := root.Get(key)
		if !value.Exists() {
			continue
		}
		next, err := sjson.SetRawBytes(out, key, []byte(value.Raw))
		if err != nil {
			return payload
		}
		out = next
	}
	return out
}

// ensureWorkBuddySystemMessage prepends a system turn when the international
// gateway would reject the payload. www.workbuddy.ai returns 400/11128
// ("first message is not system prompt") when messages[0] is not role=system.
// /v1/responses often has no instructions field, so the OpenAI translator
// emits a user turn first. Developer roles are rewritten to system before
// this runs. CN does not require a leading system turn.
func ensureWorkBuddySystemMessage(payload []byte, domain string) []byte {
	if len(payload) == 0 || !workbuddy.IsGlobalDomain(domain) {
		return payload
	}
	messages := gjson.GetBytes(payload, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return payload
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return payload
	}
	if strings.EqualFold(arr[0].Get("role").String(), "system") {
		return payload
	}

	var b strings.Builder
	b.Grow(len(messages.Raw) + 64)
	b.WriteString(`[{"role":"system","content":`)
	sysContent, _ := json.Marshal(workBuddyDefaultSystemPrompt)
	b.Write(sysContent)
	b.WriteByte('}')
	for _, msg := range arr {
		b.WriteByte(',')
		b.WriteString(msg.Raw)
	}
	b.WriteByte(']')
	out, err := sjson.SetRawBytes(payload, "messages", []byte(b.String()))
	if err != nil {
		return payload
	}
	return out
}

func rewriteWorkBuddyRequestURL(req *http.Request, domain string) {
	if req == nil || req.URL == nil {
		return
	}
	base, err := url.Parse(workbuddy.APIBaseURLForDomain(domain))
	if err != nil || base.Host == "" {
		return
	}
	req.URL.Scheme = base.Scheme
	req.URL.Host = base.Host
	req.Host = base.Host
}

// applyHeaders sets required headers for WorkBuddy API requests.
func (e *WorkBuddyExecutor) applyHeaders(req *http.Request, accessToken, userID, domain string) {
	existing := ""
	if req != nil {
		existing = req.Header.Get("X-Conversation-ID")
	}
	e.applyWorkBuddyHeaders(req, accessToken, userID, domain, existing)
}

func (e *WorkBuddyExecutor) applyWorkBuddyHeaders(req *http.Request, accessToken, userID, domain, conversationID string) {
	requestID := strings.ReplaceAll(uuid.NewString(), "-", "")
	conversationRequestID := strings.ReplaceAll(uuid.NewString(), "-", "")
	origin := workbuddy.OriginForDomain(domain)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", workbuddy.AcceptLanguageForDomain(domain))
	req.Header.Set("User-Agent", workbuddy.UserAgentForChat(domain))
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("X-User-Id", userID)
	req.Header.Set("X-Domain", domain)
	req.Header.Set("X-No-Enterprise-Id", "1")
	req.Header.Set("X-Product", workbuddy.IDEType)
	req.Header.Set("X-IDE-Type", workbuddy.IDEType)
	req.Header.Set("X-IDE-Name", workbuddy.IDEType)
	req.Header.Set("X-IDE-Version", workbuddy.AppVersion)
	req.Header.Set("X-Agent-Intent", "craft")
	req.Header.Set("X-Agent-Purpose", "conversation")
	req.Header.Set("X-Agent-Type", "main")
	req.Header.Set("X-Private-Data", "true")
	req.Header.Set("x-codebuddy-request", "1")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("X-Conversation-ID", workBuddyConversationUUID(conversationID))
	req.Header.Set("X-Conversation-Request-ID", conversationRequestID)
	req.Header.Set("X-Conversation-Message-ID", requestID)
	req.Header.Set("X-Root-Request-ID", conversationRequestID)
}

func lookupWorkBuddyModelInfo(modelID string) *registry.ModelInfo {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	if info := registry.GetGlobalRegistry().GetModelInfo(modelID, workBuddyAuthType); info != nil && strings.EqualFold(info.Type, workBuddyAuthType) {
		return info
	}
	return registry.NewWorkBuddyModelInfo(modelID, 0)
}

func applyWorkBuddyReasoningEffort(payload []byte, modelInfo *registry.ModelInfo) []byte {
	if modelInfo == nil || modelInfo.Thinking == nil || modelInfo.UserDefined {
		return payload
	}
	support := modelInfo.Thinking
	effort := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "reasoning_effort").String()))
	if effort == "" {
		if def := strings.ToLower(strings.TrimSpace(support.DefaultLevel)); def != "" {
			payload, _ = sjson.SetBytes(payload, "reasoning_effort", def)
		}
		return payload
	}
	if effort == "none" {
		if support.ZeroAllowed {
			return payload
		}
		if def := strings.ToLower(strings.TrimSpace(support.DefaultLevel)); def != "" {
			payload, _ = sjson.SetBytes(payload, "reasoning_effort", def)
		}
		return payload
	}
	if clamped, ok := clampWorkBuddyEffort(effort, support); ok && clamped != effort {
		payload, _ = sjson.SetBytes(payload, "reasoning_effort", clamped)
	}
	return payload
}

func clampWorkBuddyEffort(effort string, support *registry.ThinkingSupport) (string, bool) {
	if support == nil {
		return effort, false
	}
	levels := support.Levels
	if len(levels) == 0 && strings.TrimSpace(support.DefaultLevel) != "" {
		levels = []string{strings.ToLower(strings.TrimSpace(support.DefaultLevel))}
	}
	if len(levels) == 0 {
		return effort, false
	}
	for _, level := range levels {
		if strings.EqualFold(strings.TrimSpace(level), effort) {
			return strings.ToLower(strings.TrimSpace(level)), true
		}
	}
	order := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	pos := -1
	for i, name := range order {
		if name == effort {
			pos = i
			break
		}
	}
	if pos < 0 {
		if def := strings.ToLower(strings.TrimSpace(support.DefaultLevel)); def != "" {
			return def, true
		}
		return strings.ToLower(strings.TrimSpace(levels[0])), true
	}
	bestIdx, bestDist, bestName := -1, len(order)+1, ""
	preferHigher := pos >= 5 // xhigh/max
	for _, level := range levels {
		name := strings.ToLower(strings.TrimSpace(level))
		idx := -1
		for i, candidate := range order {
			if candidate == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		dist := idx - pos
		if dist < 0 {
			dist = -dist
		}
		better := dist < bestDist
		if dist == bestDist {
			if preferHigher {
				better = idx > bestIdx
			} else {
				better = idx < bestIdx
			}
		}
		if better {
			bestIdx, bestDist, bestName = idx, dist, name
		}
	}
	if bestName == "" {
		if def := strings.ToLower(strings.TrimSpace(support.DefaultLevel)); def != "" {
			return def, true
		}
		return strings.ToLower(strings.TrimSpace(levels[0])), true
	}
	return bestName, true
}

var workBuddyConversationNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("www.workbuddy.ai/conversation"))

func workBuddyConversationUUID(seed string) string {
	return helps.BuddyConversationUUID(workBuddyConversationNamespace, seed)
}

func resolveWorkBuddyConversationID(opts cliproxyexecutor.Options, payload []byte) string {
	return helps.ResolveBuddyConversationID(opts, payload)
}
