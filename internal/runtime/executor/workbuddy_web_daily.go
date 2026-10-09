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
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const (
	workBuddyWebDailyPrompt = "Hi"
	workBuddyWebDailyOrigin = "https://www.workbuddy.ai"
	workBuddyWebUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0"
	workBuddyWebDailyPoll   = 3 * time.Second
	workBuddyWebDailyMode   = "bypassPermissions"
	workBuddyWebIDEType     = "WorkBuddy_Web"
	workBuddyWebIDEName     = "web_agents"
)

// WorkBuddyWebDailyResult is one international web conversation that reached
// completed and produced at least one agent reply chunk.
type WorkBuddyWebDailyResult struct {
	ConversationID string
	Status         string
	Chunks         int
}

// RunWorkBuddyWebDaily opens one web-app conversation on www.workbuddy.ai and
// drives the sandbox turn until the conversation status is completed.
// It does not call /v2/chat/completions.
func RunWorkBuddyWebDaily(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, model string) (*WorkBuddyWebDailyResult, error) {
	if auth == nil {
		return nil, fmt.Errorf("workbuddy web daily: missing auth")
	}
	accessToken, userID, domain := workBuddyCredentials(auth)
	if accessToken == "" {
		return nil, fmt.Errorf("workbuddy web daily: missing access token")
	}
	if userID == "" {
		return nil, fmt.Errorf("workbuddy web daily: missing user id")
	}
	if !workbuddy.IsGlobalDomain(domain) {
		return nil, fmt.Errorf("workbuddy web daily: international accounts only")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = config.DefaultWorkBuddyWebDailyModel
	}
	if ctx == nil {
		ctx = context.Background()
	}
	client := newProxyAwareHTTPClient(ctx, cfg, auth, 0)
	result, err := runWorkBuddyWebDaily(ctx, client, workbuddy.BaseURLGlobal, accessToken, userID, model, workBuddyWebDailyPoll)
	if err != nil {
		return nil, err
	}
	log.Infof("workbuddy web daily: conversation %s completed chunks=%d", result.ConversationID, result.Chunks)
	return result, nil
}

func runWorkBuddyWebDaily(ctx context.Context, client *http.Client, apiBase, accessToken, userID, model string, pollInterval time.Duration) (*WorkBuddyWebDailyResult, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if pollInterval <= 0 {
		pollInterval = workBuddyWebDailyPoll
	}
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	conversationID, err := createWorkBuddyWebConversation(ctx, client, apiBase, accessToken, userID, model)
	if err != nil {
		return nil, err
	}
	session, err := fetchWorkBuddyWebSession(ctx, client, apiBase, accessToken, userID, conversationID)
	if err != nil {
		return nil, err
	}
	sseCtx, sseCancel := context.WithCancel(ctx)
	defer sseCancel()
	stream, connectionID, err := openWorkBuddyWebSandbox(sseCtx, client, session)
	if err != nil {
		return nil, fmt.Errorf("workbuddy web daily: conversation %s: %w", conversationID, err)
	}
	if err = driveWorkBuddyWebTurn(ctx, client, session, connectionID, model, workBuddyWebDailyPrompt); err != nil {
		return nil, fmt.Errorf("workbuddy web daily: conversation %s: %w", conversationID, err)
	}
	// The console stores the status the web client posts. Write working while the
	// turn runs, then completed after the SSE reply arrives. Waiting for GET to
	// already say completed never finishes, because that value is this write.
	if err = postWorkBuddyWebConversationStatus(ctx, client, apiBase, accessToken, userID, conversationID, "working"); err != nil {
		return nil, fmt.Errorf("workbuddy web daily: conversation %s: %w", conversationID, err)
	}
	if err = waitForWorkBuddyWebReply(ctx, stream, pollInterval); err != nil {
		return nil, fmt.Errorf("workbuddy web daily: conversation %s: %w", conversationID, err)
	}
	if err = postWorkBuddyWebConversationStatus(ctx, client, apiBase, accessToken, userID, conversationID, "completed"); err != nil {
		return nil, fmt.Errorf("workbuddy web daily: conversation %s: %w", conversationID, err)
	}
	return &WorkBuddyWebDailyResult{ConversationID: conversationID, Status: "completed", Chunks: stream.Chunks()}, nil
}

type workBuddyWebSession struct {
	Link      string
	Token     string
	SessionID string
	CWD       string
}

func createWorkBuddyWebConversation(ctx context.Context, client *http.Client, apiBase, accessToken, userID, model string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"prompt":             workBuddyWebDailyPrompt,
		"model":              model,
		"conversationOrigin": "workbuddy-app",
		"plugins": []map[string]string{{
			"name":        "weixinpay",
			"marketplace": "codebuddy-builtin",
		}},
	})
	if err != nil {
		return "", fmt.Errorf("workbuddy web daily: encode conversation: %w", err)
	}
	raw, status, err := doWorkBuddyWebJSON(ctx, client, http.MethodPost, apiBase+"/console/as/conversations/", accessToken, userID, body)
	if err != nil {
		return "", err
	}
	if !isHTTPSuccess(status) {
		return "", fmt.Errorf("workbuddy web daily: create status %d", status)
	}
	if code := gjson.GetBytes(raw, "code"); code.Exists() && code.Int() != 0 {
		return "", fmt.Errorf("workbuddy web daily: create code %d: %s", code.Int(), gjson.GetBytes(raw, "msg").String())
	}
	id := strings.TrimSpace(gjson.GetBytes(raw, "data.id").String())
	if id == "" {
		return "", fmt.Errorf("workbuddy web daily: create response missing conversation id")
	}
	return id, nil
}

func fetchWorkBuddyWebSession(ctx context.Context, client *http.Client, apiBase, accessToken, userID, conversationID string) (workBuddyWebSession, error) {
	var session workBuddyWebSession
	raw, status, err := doWorkBuddyWebJSON(ctx, client, http.MethodGet, workBuddyWebConversationURL(apiBase, conversationID)+"/session", accessToken, userID, nil)
	if err != nil {
		return session, err
	}
	if !isHTTPSuccess(status) {
		return session, fmt.Errorf("workbuddy web daily: session status %d", status)
	}
	if code := gjson.GetBytes(raw, "code"); code.Exists() && code.Int() != 0 {
		return session, fmt.Errorf("workbuddy web daily: session code %d: %s", code.Int(), gjson.GetBytes(raw, "msg").String())
	}
	data := gjson.GetBytes(raw, "data")
	session.Link = strings.TrimSpace(data.Get("link").String())
	if session.Link == "" {
		session.Link = strings.TrimSpace(data.Get("endpoint").String())
	}
	session.Token = strings.TrimSpace(data.Get("token").String())
	session.SessionID = strings.TrimSpace(data.Get("sessionId").String())
	if session.SessionID == "" {
		session.SessionID = strings.TrimSpace(data.Get("session_id").String())
	}
	if session.SessionID == "" {
		session.SessionID = conversationID
	}
	session.CWD = strings.TrimSpace(data.Get("cwd").String())
	if session.CWD == "" {
		session.CWD = "/workspace"
	}
	if session.Link == "" || session.Token == "" {
		return session, fmt.Errorf("workbuddy web daily: sandbox missing link or token")
	}
	return session, nil
}

type workBuddyWebEventStream struct {
	chunks atomic.Int32
}

func (s *workBuddyWebEventStream) Chunks() int {
	if s == nil {
		return 0
	}
	return int(s.chunks.Load())
}

func openWorkBuddyWebSandbox(ctx context.Context, client *http.Client, session workBuddyWebSession) (*workBuddyWebEventStream, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, session.Link, nil)
	if err != nil {
		return nil, "", fmt.Errorf("workbuddy web daily: sandbox request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+session.Token)
	req.Header.Set("User-Agent", workBuddyWebUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("workbuddy web daily: sandbox connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, "", fmt.Errorf("workbuddy web daily: sandbox status %d", resp.StatusCode)
	}
	connectionID := strings.TrimSpace(resp.Header.Get("Acp-Connection-Id"))
	if connectionID == "" {
		_ = resp.Body.Close()
		return nil, "", fmt.Errorf("workbuddy web daily: sandbox missing Acp-Connection-Id")
	}
	stream := &workBuddyWebEventStream{}
	go stream.read(resp.Body)
	return stream, connectionID, nil
}

func (s *workBuddyWebEventStream) read(body io.ReadCloser) {
	defer body.Close()
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.consumeSSELine(line)
		}
		if err != nil {
			return
		}
	}
}

func (s *workBuddyWebEventStream) consumeSSELine(line []byte) {
	text := strings.TrimSpace(string(line))
	if !strings.HasPrefix(text, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(text, "data:"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	if gjson.Get(payload, "method").String() != "session/update" {
		return
	}
	if gjson.Get(payload, "params.update.sessionUpdate").String() == "agent_message_chunk" {
		s.chunks.Add(1)
	}
}

func driveWorkBuddyWebTurn(ctx context.Context, client *http.Client, session workBuddyWebSession, connectionID, model, prompt string) error {
	cwd := session.CWD
	if cwd == "" {
		cwd = "/workspace"
	}
	webMeta := map[string]any{
		"model": model,
		"mode":  workBuddyWebDailyMode,
	}
	calls := []struct {
		method string
		params map[string]any
		id     int
	}{
		{
			method: "initialize",
			id:     0,
			params: map[string]any{
				"protocolVersion": 1,
				"clientCapabilities": map[string]any{
					"_meta": map[string]any{
						"codebuddy.ai": map[string]any{"cwd": cwd},
					},
					"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
					"terminal": false,
				},
			},
		},
		{
			method: "session/new",
			id:     1,
			params: map[string]any{
				"cwd":        cwd,
				"mcpServers": []any{},
			},
		},
		{
			method: "session/set_mode",
			id:     2,
			params: map[string]any{
				"sessionId": session.SessionID,
				"modeId":    workBuddyWebDailyMode,
			},
		},
		{
			method: "session/set_model",
			id:     3,
			params: map[string]any{
				"sessionId": session.SessionID,
				"modelId":   model,
			},
		},
		{
			method: "session/prompt",
			id:     4,
			params: map[string]any{
				"sessionId": session.SessionID,
				"prompt": []map[string]any{{
					"type": "text",
					"text": prompt,
					"_meta": map[string]any{
						"codebuddy.ai": webMeta,
					},
				}},
				"_meta": map[string]any{
					"codebuddy.ai": map[string]any{
						"model":         model,
						"mode":          workBuddyWebDailyMode,
						"userMessageId": fmt.Sprintf("%s-user-%d", session.SessionID, time.Now().UnixMilli()),
						"telemetryClientInfo": map[string]string{
							"ideType": workBuddyWebIDEType,
							"ideName": workBuddyWebIDEName,
						},
					},
				},
			},
		},
	}
	for _, call := range calls {
		if err := postWorkBuddyWebACP(ctx, client, session, connectionID, call.method, call.params, call.id); err != nil {
			return err
		}
	}
	return nil
}

func postWorkBuddyWebACP(ctx context.Context, client *http.Client, session workBuddyWebSession, connectionID, method string, params map[string]any, id int) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("workbuddy web daily: encode %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, session.Link, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("workbuddy web daily: %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Acp-Connection-Id", connectionID)
	req.Header.Set("Authorization", "Bearer "+session.Token)
	req.Header.Set("User-Agent", workBuddyWebUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("workbuddy web daily: %s: %w", method, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("workbuddy web daily: %s status %d", method, resp.StatusCode)
	}
	return nil
}

func waitForWorkBuddyWebReply(ctx context.Context, stream *workBuddyWebEventStream, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		pollInterval = workBuddyWebDailyPoll
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if stream.Chunks() > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("workbuddy web daily: no agent output: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func postWorkBuddyWebConversationStatus(ctx context.Context, client *http.Client, apiBase, accessToken, userID, conversationID, conversationStatus string) error {
	body, err := json.Marshal(map[string]string{"status": conversationStatus})
	if err != nil {
		return fmt.Errorf("workbuddy web daily: encode status %s: %w", conversationStatus, err)
	}
	raw, status, err := doWorkBuddyWebJSON(ctx, client, http.MethodPost, workBuddyWebConversationURL(apiBase, conversationID), accessToken, userID, body)
	if err != nil {
		return err
	}
	if !isHTTPSuccess(status) {
		return fmt.Errorf("workbuddy web daily: status %s post status %d", conversationStatus, status)
	}
	if code := gjson.GetBytes(raw, "code"); code.Exists() && code.Int() != 0 {
		return fmt.Errorf("workbuddy web daily: status %s code %d: %s", conversationStatus, code.Int(), gjson.GetBytes(raw, "msg").String())
	}
	return nil
}

func doWorkBuddyWebJSON(ctx context.Context, client *http.Client, method, endpoint, accessToken, userID string, body []byte) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, 0, fmt.Errorf("workbuddy web daily: build request: %w", err)
	}
	applyWorkBuddyWebHeaders(req, accessToken, userID)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("workbuddy web daily: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("workbuddy web daily: read response: %w", err)
	}
	return raw, resp.StatusCode, nil
}

func applyWorkBuddyWebHeaders(req *http.Request, accessToken, userID string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-User-Id", userID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", workBuddyWebDailyOrigin)
	req.Header.Set("Referer", workBuddyWebDailyOrigin+"/app")
	req.Header.Set("User-Agent", workBuddyWebUserAgent)
}

func workBuddyWebConversationURL(apiBase, conversationID string) string {
	return strings.TrimRight(apiBase, "/") + "/console/as/conversations/" + url.PathEscape(conversationID)
}
