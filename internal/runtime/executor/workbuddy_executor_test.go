package executor

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	responsesconverter "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/openai/openai/responses"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

func TestWorkBuddyChatURL_RoutesByDomain(t *testing.T) {
	t.Parallel()

	if got := workBuddyChatURL("www.workbuddy.ai"); got != workbuddy.BaseURLGlobal+"/v2/chat/completions" {
		t.Fatalf("global chat url = %s", got)
	}
	if got := workBuddyChatURL("www.workbuddy.cn"); got != workbuddy.BaseURLCN+"/v2/chat/completions" {
		t.Fatalf("cn chat url = %s", got)
	}
}

func TestRewriteWorkBuddyRequestURL_GlobalHost(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodPost, "https://www.workbuddy.cn/v2/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	rewriteWorkBuddyRequestURL(req, "www.workbuddy.ai")
	if req.URL.Host != "www.workbuddy.ai" || req.Host != "www.workbuddy.ai" {
		t.Fatalf("host = %s req.Host = %s", req.URL.Host, req.Host)
	}
	if req.URL.Path != "/v2/chat/completions" {
		t.Fatalf("path = %s", req.URL.Path)
	}
}

func TestWorkBuddyApplyHeaders_InternationalShape(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodPost, "https://www.workbuddy.ai/v2/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	exec := NewWorkBuddyExecutor(nil)
	exec.applyHeaders(req, "token", "user-1", workbuddy.DefaultDomainGlobal)

	if got := req.Header.Get("X-Domain"); got != workbuddy.DefaultDomainGlobal {
		t.Errorf("X-Domain = %s", got)
	}
	if got := req.Header.Get("X-Agent-Intent"); got != "craft" {
		t.Errorf("X-Agent-Intent = %s", got)
	}
	if got := req.Header.Get("X-Agent-Purpose"); got != "conversation" {
		t.Errorf("X-Agent-Purpose = %s", got)
	}
	if got := req.Header.Get("X-Agent-Type"); got != "main" {
		t.Errorf("X-Agent-Type = %s", got)
	}
	if got := req.Header.Get("x-codebuddy-request"); got != "1" {
		t.Errorf("x-codebuddy-request = %s", got)
	}
	if got := req.Header.Get("X-IDE-Type"); got != workbuddy.IDEType {
		t.Errorf("X-IDE-Type = %s", got)
	}
	if got := req.Header.Get("X-IDE-Version"); got != workbuddy.AppVersion {
		t.Errorf("X-IDE-Version = %s", got)
	}
	if got := req.Header.Get("X-Private-Data"); got != "true" {
		t.Errorf("X-Private-Data = %s", got)
	}
	if got := req.Header.Get("User-Agent"); got != workbuddy.UserAgentChat {
		t.Errorf("User-Agent = %s", got)
	}
	if req.Header.Get("X-Request-ID") == "" || req.Header.Get("X-Conversation-ID") == "" {
		t.Fatal("expected conversation request ids")
	}
	if got := req.Header.Get("X-Request-ID"); got != req.Header.Get("X-Conversation-Message-ID") {
		t.Fatalf("X-Request-ID %s != X-Conversation-Message-ID %s", got, req.Header.Get("X-Conversation-Message-ID"))
	}
	if req.Header.Get("X-Conversation-Request-ID") == req.Header.Get("X-Request-ID") {
		t.Fatal("X-Conversation-Request-ID should not reuse X-Request-ID")
	}
}

func TestParseWorkBuddyV3CLIModels(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"code":0,"data":{"agents":[{"name":"general-purpose","models":["ignored"]},{"name":"cli","models":["gpt-5.6-luna","kimi-k2.6",""]}]}}`)
	models, err := parseWorkBuddyV3CLIModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("len = %d, want 2", len(models))
	}
	if models[0].ID != "gpt-5.6-luna" || models[1].ID != "kimi-k2.6" {
		t.Fatalf("ids = %s, %s", models[0].ID, models[1].ID)
	}
	if models[0].Type != "workbuddy" {
		t.Fatalf("type = %s", models[0].Type)
	}
	if models[0].Thinking == nil || len(models[0].Thinking.Levels) == 0 {
		t.Fatal("expected gpt-5.6-luna thinking levels from the bundled catalog")
	}
	if models[0].ContextLength != 1000000 {
		t.Fatalf("gpt-5.6-luna context_length = %d, want 1000000", models[0].ContextLength)
	}
	if models[0].MaxCompletionTokens != 128000 {
		t.Fatalf("gpt-5.6-luna max_completion_tokens = %d, want 128000", models[0].MaxCompletionTokens)
	}
}

func TestFetchWorkBuddyModelsNilAuthUsesCNFallback(t *testing.T) {
	t.Parallel()

	got := FetchWorkBuddyModels(t.Context(), nil, nil)
	want := workBuddyFallbackModels("")
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
}

func TestWorkBuddyFallbackModels(t *testing.T) {
	t.Parallel()

	global := workBuddyFallbackModels("www.workbuddy.ai")
	if len(global) == 0 {
		t.Fatal("expected global fallback models")
	}
	foundLuna := false
	for _, model := range global {
		if model != nil && model.ID == "gpt-5.6-luna" {
			foundLuna = true
			break
		}
	}
	if !foundLuna {
		t.Fatal("expected gpt-5.6-luna in global fallback")
	}

	cn := workBuddyFallbackModels("www.workbuddy.cn")
	if len(cn) == 0 {
		t.Fatal("expected cn fallback models")
	}
}

func TestEnsureWorkBuddySystemMessage_PrependsWhenFirstIsNotSystem(t *testing.T) {
	t.Parallel()

	in := []byte(`{"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hi"}]}`)
	out := ensureWorkBuddySystemMessage(in, "www.workbuddy.ai")
	if got := gjson.GetBytes(out, "messages.#").Int(); got != 2 {
		t.Fatalf("messages len = %d, want 2; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != workBuddyDefaultSystemPrompt {
		t.Fatalf("messages.0.content = %q", got)
	}
	if got := gjson.GetBytes(out, "messages.1.role").String(); got != "user" {
		t.Fatalf("messages.1.role = %q, want user", got)
	}
	if got := gjson.GetBytes(out, "messages.1.content").String(); got != "hi" {
		t.Fatalf("messages.1.content = %q", got)
	}
}

func TestEnsureWorkBuddySystemMessage_LeavesExistingFirstSystem(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"system","content":"keep me"},{"role":"user","content":"hi"}]}`)
	out := ensureWorkBuddySystemMessage(in, "www.workbuddy.ai")
	if string(out) != string(in) {
		t.Fatalf("payload changed: %s", out)
	}
}

func TestEnsureWorkBuddySystemMessage_PrependsWhenSystemIsNotFirst(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"system","content":"later"}]}`)
	out := ensureWorkBuddySystemMessage(in, "www.workbuddy.ai")
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != workBuddyDefaultSystemPrompt {
		t.Fatalf("messages.0.content = %q", got)
	}
	if got := gjson.GetBytes(out, "messages.#").Int(); got != 3 {
		t.Fatalf("messages len = %d, want 3", got)
	}
}

func TestEnsureWorkBuddySystemMessage_SkipsCN(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out := ensureWorkBuddySystemMessage(in, "www.workbuddy.cn")
	if string(out) != string(in) {
		t.Fatalf("cn payload changed: %s", out)
	}
}

func TestPrepareWorkBuddyChatPayload_AddsCLIChatFields(t *testing.T) {
	t.Parallel()

	in := []byte(`{"model":"gpt-5.6-luna","reasoning_effort":"low","messages":[{"role":"user","content":"hi"}]}`)
	out := prepareWorkBuddyChatPayload(in, "www.workbuddy.ai", lookupWorkBuddyModelInfo("gpt-5.6-luna"))
	if !gjson.GetBytes(out, "stream").Bool() {
		t.Fatal("expected stream=true")
	}
	if !gjson.GetBytes(out, "stream_options.include_usage").Bool() {
		t.Fatal("expected stream_options.include_usage=true")
	}
	if gjson.GetBytes(out, "reasoning_summary").Exists() {
		t.Fatalf("reasoning_summary should be omitted unless the client sent it: %s", out)
	}
	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "low" {
		t.Fatalf("reasoning_effort = %q, want low", got)
	}
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system", got)
	}
}

func TestPrepareWorkBuddyChatPayload_KeepsExplicitSummary(t *testing.T) {
	t.Parallel()

	in := []byte(`{"reasoning_summary":"concise","messages":[{"role":"system","content":"sys"}]}`)
	out := prepareWorkBuddyChatPayload(in, "www.workbuddy.ai", lookupWorkBuddyModelInfo("gpt-5.6-luna"))
	if got := gjson.GetBytes(out, "reasoning_summary").String(); got != "concise" {
		t.Fatalf("reasoning_summary = %q, want concise", got)
	}
}

func TestNormalizeWorkBuddyChatStreamLine_RewritesResponseObject(t *testing.T) {
	t.Parallel()

	in := []byte(`data: {"id":"abc","object":"response","choices":[{"index":0,"delta":{"content":"hi"}}]}`)
	out := normalizeWorkBuddyChatStreamLine(in)
	payload := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(out), []byte("data:")))
	if got := gjson.GetBytes(payload, "object").String(); got != "chat.completion.chunk" {
		t.Fatalf("object = %q, want chat.completion.chunk; line=%s", got, out)
	}
	if got := gjson.GetBytes(payload, "choices.0.delta.content").String(); got != "hi" {
		t.Fatalf("content = %q", got)
	}
}

func TestNormalizeWorkBuddyChatStreamLine_StripsEmptyToolCalls(t *testing.T) {
	t.Parallel()

	in := []byte(`data: {"id":"abc","object":"response","choices":[{"index":0,"delta":{"content":"刚","reasoning_content":"","function_call":null,"tool_calls":[],"extra_fields":null},"finish_reason":""}]}`)
	out := normalizeWorkBuddyChatStreamLine(in)
	payload := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(out), []byte("data:")))
	if got := gjson.GetBytes(payload, "object").String(); got != "chat.completion.chunk" {
		t.Fatalf("object = %q", got)
	}
	if gjson.GetBytes(payload, "choices.0.delta.tool_calls").Exists() {
		t.Fatalf("empty tool_calls should be stripped: %s", payload)
	}
	if gjson.GetBytes(payload, "choices.0.delta.extra_fields").Exists() {
		t.Fatalf("extra_fields should be stripped: %s", payload)
	}
	if gjson.GetBytes(payload, "choices.0.delta.function_call").Exists() {
		t.Fatalf("null function_call should be stripped: %s", payload)
	}
	fr := gjson.GetBytes(payload, "choices.0.finish_reason")
	if fr.Exists() && fr.Type != gjson.Null && fr.String() != "" {
		t.Fatalf("empty finish_reason should be null/omitted, got %s", fr.Raw)
	}
	if got := gjson.GetBytes(payload, "choices.0.delta.content").String(); got != "刚" {
		t.Fatalf("content = %q", got)
	}
}

func TestSanitizeWorkBuddyChatPayload_DropsClientMetadata(t *testing.T) {
	t.Parallel()

	in := []byte(`{"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hi"}],"session_id":"s1","chat_id":"c1","features":{"web_search":true},"background_tasks":{"title_generation":true},"id":"owui-1"}`)
	out := prepareWorkBuddyChatPayload(in, "www.workbuddy.ai", lookupWorkBuddyModelInfo("gpt-5.6-luna"))
	if gjson.GetBytes(out, "session_id").Exists() || gjson.GetBytes(out, "chat_id").Exists() || gjson.GetBytes(out, "features").Exists() || gjson.GetBytes(out, "background_tasks").Exists() || gjson.GetBytes(out, "id").Exists() {
		t.Fatalf("client metadata leaked: %s", out)
	}
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system", got)
	}
	if !gjson.GetBytes(out, "stream").Bool() {
		t.Fatal("expected stream=true")
	}
}

func TestPrepareWorkBuddyChatPayload_ConvertsDeveloperWithoutExtraSystem(t *testing.T) {
	t.Parallel()

	in := []byte(`{"model":"gpt-5.6-luna","messages":[{"role":"developer","content":"You are pi."},{"role":"user","content":[{"type":"text","text":"hi"}]}],"store":true,"max_completion_tokens":128000}`)
	out := prepareWorkBuddyChatPayload(in, "www.workbuddy.ai", lookupWorkBuddyModelInfo("gpt-5.6-luna"))
	if gjson.GetBytes(out, "store").Exists() {
		t.Fatalf("store leaked: %s", out)
	}
	if got := gjson.GetBytes(out, "messages.#").Int(); got != 2 {
		t.Fatalf("messages len = %d, want 2; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != "You are pi." {
		t.Fatalf("messages.0.content = %q, want original developer prompt", got)
	}
	if got := gjson.GetBytes(out, "messages.1.role").String(); got != "user" {
		t.Fatalf("messages.1.role = %q", got)
	}
	for _, msg := range gjson.GetBytes(out, "messages").Array() {
		if strings.EqualFold(msg.Get("role").String(), "developer") {
			t.Fatalf("developer role leaked: %s", out)
		}
	}
}

func TestRewriteWorkBuddyDeveloperRoles_RewritesEveryDeveloperTurn(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"system","content":"sys"},{"role":"Developer","content":"dev"},{"role":"user","content":"hi"}]}`)
	out := rewriteWorkBuddyDeveloperRoles(in)
	if got := gjson.GetBytes(out, "messages.1.role").String(); got != "system" {
		t.Fatalf("messages.1.role = %q, want system; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.content").String(); got != "dev" {
		t.Fatalf("messages.1.content = %q", got)
	}
}

func TestPrepareWorkBuddyChatPayload_ConvertsDeveloperOnCN(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"developer","content":"cn-dev"},{"role":"user","content":"hi"}]}`)
	out := prepareWorkBuddyChatPayload(in, "www.workbuddy.cn", nil)
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.#").Int(); got != 2 {
		t.Fatalf("cn should not prepend an extra system turn: %s", out)
	}
}

func TestIsWorkBuddyTransientNetworkError(t *testing.T) {
	t.Parallel()

	if !isWorkBuddyTransientNetworkError(errors.New(`Post "https://www.workbuddy.ai/v2/chat/completions": net/http: TLS handshake timeout`)) {
		t.Fatal("expected TLS handshake timeout to be transient")
	}
	if isWorkBuddyTransientNetworkError(context.Canceled) {
		t.Fatal("canceled is not transient")
	}
	if isWorkBuddyTransientNetworkError(context.DeadlineExceeded) {
		t.Fatal("deadline exceeded is not transient")
	}
	if isWorkBuddyTransientNetworkError(errors.New("status 400")) {
		t.Fatal("400 is not a transient network error")
	}
}

func TestNormalizeWorkBuddyChatStreamLine_LeavesDoneAndChunk(t *testing.T) {
	t.Parallel()

	done := []byte("data: [DONE]")
	if got := normalizeWorkBuddyChatStreamLine(done); string(got) != string(done) {
		t.Fatalf("DONE changed: %s", got)
	}
	chunk := []byte(`data: {"object":"chat.completion.chunk","choices":[]}`)
	if got := normalizeWorkBuddyChatStreamLine(chunk); string(got) != string(chunk) {
		t.Fatalf("chunk object changed: %s", got)
	}
}

func TestIsWorkBuddyTransientProviderError(t *testing.T) {
	t.Parallel()

	body := []byte(`{"code":11134,"msg":"the model provider is temporarily unavailable, please retry later or switch to another model"}`)
	if !isWorkBuddyTransientProviderError(http.StatusInternalServerError, body) {
		t.Fatal("expected 11134 to be transient")
	}
	short := []byte(`{"type":"error","code":"11134"}`)
	if !isWorkBuddyTransientProviderError(http.StatusInternalServerError, short) {
		t.Fatal("expected string 11134 to be transient")
	}
	if isWorkBuddyTransientProviderError(http.StatusBadRequest, []byte(`{"code":11128,"msg":"first message is not system prompt"}`)) {
		t.Fatal("11128 is not transient")
	}
}

func TestNormalizeWorkBuddyChatStreamLine_ResponsesTranslatorAcceptsNormalizedChunk(t *testing.T) {
	t.Parallel()

	line := []byte(`data: {"id":"cmb-1","object":"response","created":1,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{"content":"PONG"},"finish_reason":""}]}`)
	var ignored any
	if chunks := responsesconverter.ConvertOpenAIChatCompletionsResponseToOpenAIResponses(t.Context(), "gpt-5.6-luna", nil, nil, line, &ignored); len(chunks) != 0 {
		t.Fatalf("raw object=response should be dropped, got %d chunks", len(chunks))
	}

	var param any
	chunks := responsesconverter.ConvertOpenAIChatCompletionsResponseToOpenAIResponses(t.Context(), "gpt-5.6-luna", nil, nil, normalizeWorkBuddyChatStreamLine(line), &param)
	if len(chunks) == 0 {
		t.Fatal("expected responses SSE after normalizing WorkBuddy object=response")
	}
	joined := string(bytes.Join(chunks, nil))
	if !strings.Contains(joined, "response.created") {
		t.Fatalf("missing response.created in %s", joined)
	}
}

func TestEnsureWorkBuddySystemMessage_LegacyWorkBuddy(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	out := ensureWorkBuddySystemMessage(in, "www.workbuddy.ai")
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.content.0.text").String(); got != "hi" {
		t.Fatalf("user text = %q", got)
	}
}

func TestWorkBuddyConversationUUID_StickyFromSeed(t *testing.T) {
	t.Parallel()

	a := workBuddyConversationUUID("ctx:v1:abc")
	b := workBuddyConversationUUID("ctx:v1:abc")
	c := workBuddyConversationUUID("ctx:v1:xyz")
	if a != b {
		t.Fatalf("same seed produced %s and %s", a, b)
	}
	if a == c {
		t.Fatal("different seeds produced the same conversation id")
	}
	if _, err := uuid.Parse(a); err != nil {
		t.Fatalf("conversation id is not a UUID: %v", err)
	}
	explicit := "70eba61f-67d5-41a1-aa6a-71f416175d73"
	if got := workBuddyConversationUUID(explicit); got != explicit {
		t.Fatalf("explicit UUID = %s, want %s", got, explicit)
	}
}

func TestWorkBuddyApplyHeaders_StickyConversation(t *testing.T) {
	t.Parallel()

	exec := NewWorkBuddyExecutor(nil)
	req1, err := http.NewRequest(http.MethodPost, "https://www.workbuddy.ai/v2/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req2, err := http.NewRequest(http.MethodPost, "https://www.workbuddy.ai/v2/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	exec.applyWorkBuddyHeaders(req1, "token", "user-1", workbuddy.DefaultDomainGlobal, "ctx:v1:same-session")
	exec.applyWorkBuddyHeaders(req2, "token", "user-1", workbuddy.DefaultDomainGlobal, "ctx:v1:same-session")

	if req1.Header.Get("X-Conversation-ID") != req2.Header.Get("X-Conversation-ID") {
		t.Fatalf("conversation id not sticky: %s vs %s", req1.Header.Get("X-Conversation-ID"), req2.Header.Get("X-Conversation-ID"))
	}
	if req1.Header.Get("X-Request-ID") == req2.Header.Get("X-Request-ID") {
		t.Fatal("request id should change per call")
	}
	if req1.Header.Get("X-Conversation-Request-ID") == req1.Header.Get("X-Request-ID") {
		t.Fatal("conversation request id should not equal request id")
	}
}

func TestResolveWorkBuddyConversationID_PrefersExplicitThenDerived(t *testing.T) {
	t.Parallel()

	opts := cliproxyexecutor.Options{
		Headers:         http.Header{"X-Conversation-ID": []string{"70eba61f-67d5-41a1-aa6a-71f416175d73"}},
		OriginalRequest: []byte(`{"session_id":"payload-session"}`),
		Metadata: map[string]any{
			cliproxyexecutor.DerivedSessionIDMetadataKey: "ctx:v1:derived",
		},
	}
	if got := resolveWorkBuddyConversationID(opts, nil); got != "70eba61f-67d5-41a1-aa6a-71f416175d73" {
		t.Fatalf("header conversation id = %q", got)
	}

	opts.Headers = nil
	if got := resolveWorkBuddyConversationID(opts, nil); got != "payload-session" {
		t.Fatalf("payload session id = %q", got)
	}

	opts.OriginalRequest = nil
	if got := resolveWorkBuddyConversationID(opts, nil); got != "ctx:v1:derived" {
		t.Fatalf("derived session id = %q", got)
	}
}

func TestPrepareWorkBuddyChatPayload_DefaultAndClampEffort(t *testing.T) {
	t.Parallel()

	hy3 := lookupWorkBuddyModelInfo("hy3")
	if hy3 == nil || hy3.Thinking == nil {
		t.Fatal("expected hy3 catalog thinking")
	}

	out := prepareWorkBuddyChatPayload([]byte(`{"model":"hy3","messages":[{"role":"system","content":"sys"}]}`), "www.workbuddy.ai", hy3)
	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "high" {
		t.Fatalf("default hy3 effort = %q, want high; body=%s", got, out)
	}
	if gjson.GetBytes(out, "reasoning_summary").Exists() {
		t.Fatalf("reasoning_summary should stay omitted: %s", out)
	}

	clamped := prepareWorkBuddyChatPayload([]byte(`{"model":"hy3","reasoning_effort":"medium","messages":[{"role":"system","content":"sys"}]}`), "www.workbuddy.ai", hy3)
	if got := gjson.GetBytes(clamped, "reasoning_effort").String(); got != "low" {
		t.Fatalf("hy3 medium should clamp to low, got %q; body=%s", got, clamped)
	}

	none := prepareWorkBuddyChatPayload([]byte(`{"model":"hy3","reasoning_effort":"none","messages":[{"role":"system","content":"sys"}]}`), "www.workbuddy.ai", hy3)
	if got := gjson.GetBytes(none, "reasoning_effort").String(); got != "high" {
		t.Fatalf("hy3 cannot disable thinking, got %q; body=%s", got, none)
	}
}

func TestParseWorkBuddyV3CLIModels_KeepsUnknownCLIModelsWhenLiveMetaIsWeird(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"code":0,"data":{"agents":[{"name":"cli","models":["deepseek-v4.1-flash","hy3"]}],"models":[
		{"id":"hy3","name":"Hy3","maxInputTokens":192000.5,"maxOutputTokens":64000,"supportsReasoning":true,"reasoning":{"defaultEffort":"high","supportedEfforts":["low","high"]}},
		"not-an-object",
		{"id":"broken","maxInputTokens":"1m"}
	]}}`)
	models, err := parseWorkBuddyV3CLIModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("len = %d, want 2 (cli ids must survive live overlay)", len(models))
	}
	if models[0].ID != "deepseek-v4.1-flash" {
		t.Fatalf("ids[0] = %s, want deepseek-v4.1-flash", models[0].ID)
	}
	if models[0].Type != "workbuddy" {
		t.Fatalf("type = %s", models[0].Type)
	}
	if !models[0].UserDefined {
		t.Fatal("unknown live IDs should stay user-defined")
	}
	if models[1].ID != "hy3" {
		t.Fatalf("ids[1] = %s, want hy3", models[1].ID)
	}
	if models[1].ContextLength != 192000 {
		t.Fatalf("hy3 context = %d, want 192000 from live overlay", models[1].ContextLength)
	}
}

func TestParseWorkBuddyV3CLIModels_AppliesLiveMeta(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"code":0,"data":{"agents":[{"name":"cli","models":["hy3","gpt-5.6-luna","default-model"]}],"models":[
		{"id":"hy3","name":"Hy3","maxInputTokens":192000,"maxOutputTokens":64000,"maxAllowedSize":192000,"onlyReasoning":true,"supportsReasoning":true,"reasoning":{"canDisableThinking":false,"defaultEffort":"high","summary":"auto","supportedEfforts":["low","high"]}},
		{"id":"gpt-5.6-luna","name":"GPT-5.6-Luna","maxInputTokens":1000000,"maxOutputTokens":128000,"maxAllowedSize":1000000,"supportsReasoning":true,"reasoning":{"canDisableThinking":true,"defaultEffort":"high","supportedEfforts":["low","medium","high","xhigh","max"]}},
		{"id":"default-model","name":"Auto","maxInputTokens":176000,"maxOutputTokens":24000,"maxAllowedSize":200000}
	]}}`)
	models, err := parseWorkBuddyV3CLIModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("len = %d, want 3", len(models))
	}
	hy3 := models[0]
	if hy3.ContextLength != 192000 || hy3.MaxCompletionTokens != 64000 {
		t.Fatalf("hy3 window = %d/%d", hy3.ContextLength, hy3.MaxCompletionTokens)
	}
	if hy3.Thinking == nil || hy3.Thinking.DefaultLevel != "high" || hy3.Thinking.ZeroAllowed {
		t.Fatalf("hy3 thinking = %#v", hy3.Thinking)
	}
	luna := models[1]
	if luna.ContextLength != 1000000 || luna.MaxCompletionTokens != 128000 {
		t.Fatalf("luna window = %d/%d", luna.ContextLength, luna.MaxCompletionTokens)
	}
	if luna.Thinking == nil || !luna.Thinking.ZeroAllowed || luna.Thinking.DefaultLevel != "high" {
		t.Fatalf("luna thinking = %#v", luna.Thinking)
	}
	auto := models[2]
	if auto.Thinking != nil {
		t.Fatalf("default-model should not advertise thinking: %#v", auto.Thinking)
	}
	if auto.ContextLength != 176000 {
		t.Fatalf("default-model context = %d, want 176000", auto.ContextLength)
	}
}
