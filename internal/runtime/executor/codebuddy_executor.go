package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	codeBuddyChatPath                 = "/v2/chat/completions"
	codeBuddyAuthType                 = "codebuddy"
	codeBuddyDefaultSystemPrompt      = "You are a helpful assistant."
	codeBuddyDefaultReasoningSummary  = "auto"
	codeBuddyTransientProviderRetries = 1
)

// Official CLI chat bodies only send these fields. Open WebUI and similar
// clients attach session/chat metadata that www.codebuddy.ai rejects with
// 400/11128 ("Illegal API invocation from an unapproved channel").
var codeBuddyChatAllowedFields = []string{
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

// CodeBuddyExecutor handles requests to the CodeBuddy API.
type CodeBuddyExecutor struct {
	cfg *config.Config
}

// NewCodeBuddyExecutor creates a new CodeBuddy executor instance.
func NewCodeBuddyExecutor(cfg *config.Config) *CodeBuddyExecutor {
	return &CodeBuddyExecutor{cfg: cfg}
}

// Identifier returns the unique identifier for this executor.
func (e *CodeBuddyExecutor) Identifier() string { return codeBuddyAuthType }

// codeBuddyCredentials extracts the access token and domain from auth metadata.
func codeBuddyCredentials(auth *cliproxyauth.Auth) (accessToken, userID, domain string) {
	if auth == nil {
		return "", "", ""
	}
	accessToken = metaStringValue(auth.Metadata, "access_token")
	userID = metaStringValue(auth.Metadata, "user_id")
	domain = metaStringValue(auth.Metadata, "domain")
	if domain == "" {
		domain = codebuddy.DefaultDomain
	}
	return
}

// PrepareRequest prepares the HTTP request before execution.
func (e *CodeBuddyExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	accessToken, userID, domain := codeBuddyCredentials(auth)
	if accessToken == "" {
		return fmt.Errorf("codebuddy: missing access token")
	}
	e.applyHeaders(req, accessToken, userID, domain)
	rewriteCodeBuddyRequestURL(req, domain)
	return nil
}

// HttpRequest executes a raw HTTP request.
func (e *CodeBuddyExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("codebuddy executor: request is nil")
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
func (e *CodeBuddyExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := newUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.trackFailure(ctx, &err)

	accessToken, userID, domain := codeBuddyCredentials(auth)
	if accessToken == "" {
		return resp, fmt.Errorf("codebuddy: missing access token")
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

	translated, err = thinking.ApplyThinking(translated, req.Model, from.String(), to.String(), e.Identifier())
	if err != nil {
		return resp, err
	}
	translated = prepareCodeBuddyChatPayload(translated, domain)

	httpResp, err := e.doCodeBuddyChat(ctx, auth, accessToken, userID, domain, translated)
	if err != nil {
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("codebuddy executor: close response body error: %v", errClose)
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
func (e *CodeBuddyExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := newUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.trackFailure(ctx, &err)

	accessToken, userID, domain := codeBuddyCredentials(auth)
	if accessToken == "" {
		return nil, fmt.Errorf("codebuddy: missing access token")
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

	translated, err = thinking.ApplyThinking(translated, req.Model, from.String(), to.String(), e.Identifier())
	if err != nil {
		return nil, err
	}
	translated = prepareCodeBuddyChatPayload(translated, domain)

	httpResp, err := e.doCodeBuddyChat(ctx, auth, accessToken, userID, domain, translated)
	if err != nil {
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("codebuddy executor: close stream body error: %v", errClose)
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
			normalized := normalizeCodeBuddyChatStreamLine(bytes.Clone(line))
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

// Refresh exchanges the CodeBuddy refresh token for a new access token.
func (e *CodeBuddyExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	if auth == nil {
		return nil, fmt.Errorf("codebuddy: missing auth")
	}

	refreshToken := metaStringValue(auth.Metadata, "refresh_token")
	if refreshToken == "" {
		log.Debugf("codebuddy executor: no refresh token available, skipping refresh")
		return auth, nil
	}

	accessToken, userID, domain := codeBuddyCredentials(auth)

	authSvc := codebuddy.NewCodeBuddyAuth(e.cfg)
	storage, err := authSvc.RefreshToken(ctx, accessToken, refreshToken, userID, domain)
	if err != nil {
		return nil, fmt.Errorf("codebuddy: token refresh failed: %w", err)
	}

	updated := auth.Clone()
	updated.Metadata["access_token"] = storage.AccessToken
	if storage.RefreshToken != "" {
		updated.Metadata["refresh_token"] = storage.RefreshToken
	}
	updated.Metadata["expires_in"] = storage.ExpiresIn
	updated.Metadata["domain"] = storage.Domain
	if storage.UserID != "" {
		updated.Metadata["user_id"] = storage.UserID
	}
	now := time.Now()
	updated.UpdatedAt = now
	updated.LastRefreshedAt = now

	return updated, nil
}

// CountTokens is not supported for CodeBuddy.
func (e *CodeBuddyExecutor) CountTokens(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, fmt.Errorf("codebuddy: count tokens not supported")
}

func codeBuddyChatURL(domain string) string {
	return codebuddy.APIBaseURLForDomain(domain) + codeBuddyChatPath
}

func (e *CodeBuddyExecutor) doCodeBuddyChat(ctx context.Context, auth *cliproxyauth.Auth, accessToken, userID, domain string, body []byte) (*http.Response, error) {
	url := codeBuddyChatURL(domain)
	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}

	var lastErr error
	for attempt := 0; attempt <= codeBuddyTransientProviderRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		e.applyHeaders(httpReq, accessToken, userID, domain)
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
			return nil, err
		}
		recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
		if isHTTPSuccess(httpResp.StatusCode) {
			return httpResp, nil
		}

		errBody, _ := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("codebuddy executor: close error response body: %v", errClose)
		}
		appendAPIResponseChunk(ctx, e.cfg, errBody)
		summary := summarizeErrorBody(httpResp.Header.Get("Content-Type"), errBody)
		lastErr = statusErr{code: httpResp.StatusCode, msg: string(errBody)}
		if attempt < codeBuddyTransientProviderRetries && isCodeBuddyTransientProviderError(httpResp.StatusCode, errBody) {
			log.Warnf("codebuddy executor: transient upstream error status: %d, body: %s; retrying", httpResp.StatusCode, summary)
			continue
		}
		log.Warnf("codebuddy executor: upstream error status: %d, body: %s", httpResp.StatusCode, summary)
		return nil, lastErr
	}
	return nil, lastErr
}

func isCodeBuddyTransientProviderError(status int, body []byte) bool {
	if status != http.StatusInternalServerError && status != http.StatusBadGateway && status != http.StatusServiceUnavailable {
		return false
	}
	code := gjson.GetBytes(body, "code")
	if code.Int() == 11134 || code.String() == "11134" {
		return true
	}
	msg := strings.ToLower(strings.Join([]string{
		code.String(),
		gjson.GetBytes(body, "msg").String(),
		gjson.GetBytes(body, "message").String(),
		gjson.GetBytes(body, "extError.code").String(),
		gjson.GetBytes(body, "extError.message").String(),
	}, " "))
	return strings.Contains(msg, "temporarily unavailable") || strings.Contains(msg, "service_unavailable")
}

// normalizeCodeBuddyChatStreamLine rewrites CodeBuddy international SSE chunks.
// www.codebuddy.ai /v2/chat/completions streams chat-style choices/delta payloads
// with object="response". The OpenAI Responses translator only accepts
// object="chat.completion.chunk", so unnormalized streams look empty and /v1/responses
// fails after the upstream 200.
func normalizeCodeBuddyChatStreamLine(line []byte) []byte {
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

// prepareCodeBuddyChatPayload matches the international CLI chat body:
// stream + include_usage, reasoning_summary=auto, and a leading system turn.
func prepareCodeBuddyChatPayload(payload []byte, domain string) []byte {
	if len(payload) == 0 {
		return payload
	}
	payload, _ = sjson.SetBytes(payload, "stream", true)
	payload, _ = sjson.SetBytes(payload, "stream_options.include_usage", true)
	if !gjson.GetBytes(payload, "reasoning_summary").Exists() {
		payload, _ = sjson.SetBytes(payload, "reasoning_summary", codeBuddyDefaultReasoningSummary)
	}
	payload = ensureCodeBuddySystemMessage(payload, domain)
	return sanitizeCodeBuddyChatPayload(payload)
}

func sanitizeCodeBuddyChatPayload(payload []byte) []byte {
	root := gjson.ParseBytes(payload)
	if !root.IsObject() {
		return payload
	}
	out := []byte(`{}`)
	for _, key := range codeBuddyChatAllowedFields {
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

// ensureCodeBuddySystemMessage prepends a system turn when the international
// gateway would reject the payload. www.codebuddy.ai returns 400/11128
// ("first message is not system prompt") when messages[0] is not role=system.
// /v1/responses often has no instructions field, so the OpenAI translator
// emits a user/developer turn first. CN does not require this.
func ensureCodeBuddySystemMessage(payload []byte, domain string) []byte {
	if len(payload) == 0 || !codebuddy.IsGlobalDomain(domain) {
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
	sysContent, _ := json.Marshal(codeBuddyDefaultSystemPrompt)
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

func rewriteCodeBuddyRequestURL(req *http.Request, domain string) {
	if req == nil || req.URL == nil {
		return
	}
	base, err := url.Parse(codebuddy.APIBaseURLForDomain(domain))
	if err != nil || base.Host == "" {
		return
	}
	req.URL.Scheme = base.Scheme
	req.URL.Host = base.Host
	req.Host = base.Host
}

// applyHeaders sets required headers for CodeBuddy API requests.
func (e *CodeBuddyExecutor) applyHeaders(req *http.Request, accessToken, userID, domain string) {
	requestID := strings.ReplaceAll(uuid.NewString(), "-", "")
	conversationID := uuid.NewString()
	messageID := strings.ReplaceAll(uuid.NewString(), "-", "")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codebuddy.UserAgent)
	req.Header.Set("X-User-Id", userID)
	req.Header.Set("X-Domain", domain)
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-IDE-Type", "CLI")
	req.Header.Set("X-IDE-Name", "CLI")
	req.Header.Set("X-IDE-Version", codebuddy.ClientVersion)
	req.Header.Set("X-Agent-Intent", "craft")
	req.Header.Set("X-Agent-Purpose", "conversation")
	req.Header.Set("X-Agent-Type", "main")
	req.Header.Set("X-Private-Data", "false")
	req.Header.Set("x-codebuddy-request", "1")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("X-Conversation-ID", conversationID)
	req.Header.Set("X-Conversation-Request-ID", requestID)
	req.Header.Set("X-Conversation-Message-ID", messageID)
}

type openAIChatStreamChoiceAccumulator struct {
	Role               string
	ContentParts       []string
	ReasoningParts     []string
	FinishReason       string
	ToolCalls          map[int]*openAIChatStreamToolCallAccumulator
	ToolCallOrder      []int
	NativeFinishReason any
}

type openAIChatStreamToolCallAccumulator struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}

func aggregateOpenAIChatCompletionStream(raw []byte) ([]byte, usage.Detail, error) {
	lines := bytes.Split(raw, []byte("\n"))
	var (
		responseID  string
		model       string
		created     int64
		serviceTier string
		systemFP    string
		usageDetail usage.Detail
		choices     = map[int]*openAIChatStreamChoiceAccumulator{}
		choiceOrder []int
	)

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[5:])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		if !gjson.ValidBytes(payload) {
			continue
		}

		root := gjson.ParseBytes(payload)
		if responseID == "" {
			responseID = root.Get("id").String()
		}
		if model == "" {
			model = root.Get("model").String()
		}
		if created == 0 {
			created = root.Get("created").Int()
		}
		if serviceTier == "" {
			serviceTier = root.Get("service_tier").String()
		}
		if systemFP == "" {
			systemFP = root.Get("system_fingerprint").String()
		}
		if detail, ok := parseOpenAIStreamUsage(line); ok {
			usageDetail = detail
		}

		for _, choiceResult := range root.Get("choices").Array() {
			idx := int(choiceResult.Get("index").Int())
			choice := choices[idx]
			if choice == nil {
				choice = &openAIChatStreamChoiceAccumulator{ToolCalls: map[int]*openAIChatStreamToolCallAccumulator{}}
				choices[idx] = choice
				choiceOrder = append(choiceOrder, idx)
			}

			delta := choiceResult.Get("delta")
			if role := delta.Get("role").String(); role != "" {
				choice.Role = role
			}
			if content := delta.Get("content").String(); content != "" {
				choice.ContentParts = append(choice.ContentParts, content)
			}
			if reasoning := delta.Get("reasoning_content").String(); reasoning != "" {
				choice.ReasoningParts = append(choice.ReasoningParts, reasoning)
			}
			if finishReason := choiceResult.Get("finish_reason").String(); finishReason != "" {
				choice.FinishReason = finishReason
			}
			if nativeFinishReason := choiceResult.Get("native_finish_reason"); nativeFinishReason.Exists() {
				choice.NativeFinishReason = nativeFinishReason.Value()
			}

			for _, toolCallResult := range delta.Get("tool_calls").Array() {
				toolIdx := int(toolCallResult.Get("index").Int())
				toolCall := choice.ToolCalls[toolIdx]
				if toolCall == nil {
					toolCall = &openAIChatStreamToolCallAccumulator{}
					choice.ToolCalls[toolIdx] = toolCall
					choice.ToolCallOrder = append(choice.ToolCallOrder, toolIdx)
				}
				if id := toolCallResult.Get("id").String(); id != "" {
					toolCall.ID = id
				}
				if typ := toolCallResult.Get("type").String(); typ != "" {
					toolCall.Type = typ
				}
				if name := toolCallResult.Get("function.name").String(); name != "" {
					toolCall.Name = name
				}
				if args := toolCallResult.Get("function.arguments").String(); args != "" {
					toolCall.Arguments.WriteString(args)
				}
			}
		}
	}

	if responseID == "" && model == "" && len(choiceOrder) == 0 {
		return nil, usageDetail, fmt.Errorf("codebuddy: streaming response did not contain any chat completion chunks")
	}

	response := map[string]any{
		"id":      responseID,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": make([]map[string]any, 0, len(choiceOrder)),
		"usage": map[string]any{
			"prompt_tokens":     usageDetail.InputTokens,
			"completion_tokens": usageDetail.OutputTokens,
			"total_tokens":      usageDetail.TotalTokens,
		},
	}
	if serviceTier != "" {
		response["service_tier"] = serviceTier
	}
	if systemFP != "" {
		response["system_fingerprint"] = systemFP
	}

	for _, idx := range choiceOrder {
		choice := choices[idx]
		message := map[string]any{
			"role":    choice.Role,
			"content": strings.Join(choice.ContentParts, ""),
		}
		if message["role"] == "" {
			message["role"] = "assistant"
		}
		if len(choice.ReasoningParts) > 0 {
			message["reasoning_content"] = strings.Join(choice.ReasoningParts, "")
		}
		if len(choice.ToolCallOrder) > 0 {
			toolCalls := make([]map[string]any, 0, len(choice.ToolCallOrder))
			for _, toolIdx := range choice.ToolCallOrder {
				toolCall := choice.ToolCalls[toolIdx]
				toolCallType := toolCall.Type
				if toolCallType == "" {
					toolCallType = "function"
				}
				arguments := toolCall.Arguments.String()
				if arguments == "" {
					arguments = "{}"
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   toolCall.ID,
					"type": toolCallType,
					"function": map[string]any{
						"name":      toolCall.Name,
						"arguments": arguments,
					},
				})
			}
			message["tool_calls"] = toolCalls
		}

		finishReason := choice.FinishReason
		if finishReason == "" {
			finishReason = "stop"
		}
		choicePayload := map[string]any{
			"index":         idx,
			"message":       message,
			"finish_reason": finishReason,
		}
		if choice.NativeFinishReason != nil {
			choicePayload["native_finish_reason"] = choice.NativeFinishReason
		}
		response["choices"] = append(response["choices"].([]map[string]any), choicePayload)
	}

	out, err := json.Marshal(response)
	if err != nil {
		return nil, usageDetail, fmt.Errorf("codebuddy: failed to encode aggregated response: %w", err)
	}
	return out, usageDetail, nil
}
