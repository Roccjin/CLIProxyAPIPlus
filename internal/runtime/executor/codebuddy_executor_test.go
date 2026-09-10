package executor

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	responsesconverter "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/openai/openai/responses"
	"github.com/tidwall/gjson"
)

func TestCodeBuddyChatURL_RoutesByDomain(t *testing.T) {
	t.Parallel()

	if got := codeBuddyChatURL("www.codebuddy.ai"); got != codebuddy.BaseURLGlobal+"/v2/chat/completions" {
		t.Fatalf("global chat url = %s", got)
	}
	if got := codeBuddyChatURL("www.codebuddy.cn"); got != codebuddy.BaseURLCN+"/v2/chat/completions" {
		t.Fatalf("cn chat url = %s", got)
	}
}

func TestRewriteCodeBuddyRequestURL_GlobalHost(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodPost, "https://copilot.tencent.com/v2/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	rewriteCodeBuddyRequestURL(req, "www.codebuddy.ai")
	if req.URL.Host != "www.codebuddy.ai" || req.Host != "www.codebuddy.ai" {
		t.Fatalf("host = %s req.Host = %s", req.URL.Host, req.Host)
	}
	if req.URL.Path != "/v2/chat/completions" {
		t.Fatalf("path = %s", req.URL.Path)
	}
}

func TestCodeBuddyApplyHeaders_InternationalShape(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodPost, "https://www.codebuddy.ai/v2/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	exec := NewCodeBuddyExecutor(nil)
	exec.applyHeaders(req, "token", "user-1", codebuddy.DefaultDomainGlobal)

	if got := req.Header.Get("X-Domain"); got != codebuddy.DefaultDomainGlobal {
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
	if got := req.Header.Get("X-IDE-Version"); got != codebuddy.ClientVersion {
		t.Errorf("X-IDE-Version = %s", got)
	}
	if got := req.Header.Get("User-Agent"); got != codebuddy.UserAgent {
		t.Errorf("User-Agent = %s", got)
	}
	if req.Header.Get("X-Request-ID") == "" || req.Header.Get("X-Conversation-ID") == "" {
		t.Fatal("expected conversation request ids")
	}
}

func TestParseCodeBuddyV3CLIModels(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"code":0,"data":{"agents":[{"name":"general-purpose","models":["ignored"]},{"name":"cli","models":["gpt-5.6-luna","kimi-k2.6",""]}]}}`)
	models, err := parseCodeBuddyV3CLIModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("len = %d, want 2", len(models))
	}
	if models[0].ID != "gpt-5.6-luna" || models[1].ID != "kimi-k2.6" {
		t.Fatalf("ids = %s, %s", models[0].ID, models[1].ID)
	}
	if models[0].Type != "codebuddy" {
		t.Fatalf("type = %s", models[0].Type)
	}
	if models[0].Thinking == nil || len(models[0].Thinking.Levels) == 0 {
		t.Fatal("expected gpt-5.6-luna thinking levels from the bundled catalog")
	}
	if models[0].ContextLength < 272000 {
		t.Fatalf("gpt-5.6-luna context_length = %d, want at least 272000", models[0].ContextLength)
	}
}

func TestFetchCodeBuddyModelsNilAuthUsesCNFallback(t *testing.T) {
	t.Parallel()

	got := FetchCodeBuddyModels(t.Context(), nil, nil)
	want := codeBuddyFallbackModels("")
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
}

func TestCodeBuddyFallbackModels(t *testing.T) {
	t.Parallel()

	global := codeBuddyFallbackModels("www.codebuddy.ai")
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

	cn := codeBuddyFallbackModels("www.codebuddy.cn")
	if len(cn) == 0 {
		t.Fatal("expected cn fallback models")
	}
}

func TestEnsureCodeBuddySystemMessage_PrependsWhenFirstIsNotSystem(t *testing.T) {
	t.Parallel()

	in := []byte(`{"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hi"}]}`)
	out := ensureCodeBuddySystemMessage(in, "www.codebuddy.ai")
	if got := gjson.GetBytes(out, "messages.#").Int(); got != 2 {
		t.Fatalf("messages len = %d, want 2; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system", got)
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != codeBuddyDefaultSystemPrompt {
		t.Fatalf("messages.0.content = %q", got)
	}
	if got := gjson.GetBytes(out, "messages.1.role").String(); got != "user" {
		t.Fatalf("messages.1.role = %q, want user", got)
	}
	if got := gjson.GetBytes(out, "messages.1.content").String(); got != "hi" {
		t.Fatalf("messages.1.content = %q", got)
	}
}

func TestEnsureCodeBuddySystemMessage_LeavesExistingFirstSystem(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"system","content":"keep me"},{"role":"user","content":"hi"}]}`)
	out := ensureCodeBuddySystemMessage(in, "www.codebuddy.ai")
	if string(out) != string(in) {
		t.Fatalf("payload changed: %s", out)
	}
}

func TestEnsureCodeBuddySystemMessage_PrependsWhenSystemIsNotFirst(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"system","content":"later"}]}`)
	out := ensureCodeBuddySystemMessage(in, "www.codebuddy.ai")
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.0.content").String(); got != codeBuddyDefaultSystemPrompt {
		t.Fatalf("messages.0.content = %q", got)
	}
	if got := gjson.GetBytes(out, "messages.#").Int(); got != 3 {
		t.Fatalf("messages len = %d, want 3", got)
	}
}

func TestEnsureCodeBuddySystemMessage_SkipsCN(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out := ensureCodeBuddySystemMessage(in, "www.codebuddy.cn")
	if string(out) != string(in) {
		t.Fatalf("cn payload changed: %s", out)
	}
}

func TestPrepareCodeBuddyChatPayload_AddsCLIChatFields(t *testing.T) {
	t.Parallel()

	in := []byte(`{"model":"gpt-5.6-luna","reasoning_effort":"low","messages":[{"role":"user","content":"hi"}]}`)
	out := prepareCodeBuddyChatPayload(in, "www.codebuddy.ai")
	if !gjson.GetBytes(out, "stream").Bool() {
		t.Fatal("expected stream=true")
	}
	if !gjson.GetBytes(out, "stream_options.include_usage").Bool() {
		t.Fatal("expected stream_options.include_usage=true")
	}
	if got := gjson.GetBytes(out, "reasoning_summary").String(); got != codeBuddyDefaultReasoningSummary {
		t.Fatalf("reasoning_summary = %q, want %q", got, codeBuddyDefaultReasoningSummary)
	}
	if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "low" {
		t.Fatalf("reasoning_effort = %q, want low", got)
	}
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system", got)
	}
}

func TestPrepareCodeBuddyChatPayload_KeepsExplicitSummary(t *testing.T) {
	t.Parallel()

	in := []byte(`{"reasoning_summary":"concise","messages":[{"role":"system","content":"sys"}]}`)
	out := prepareCodeBuddyChatPayload(in, "www.codebuddy.ai")
	if got := gjson.GetBytes(out, "reasoning_summary").String(); got != "concise" {
		t.Fatalf("reasoning_summary = %q, want concise", got)
	}
}

func TestNormalizeCodeBuddyChatStreamLine_RewritesResponseObject(t *testing.T) {
	t.Parallel()

	in := []byte(`data: {"id":"abc","object":"response","choices":[{"index":0,"delta":{"content":"hi"}}]}`)
	out := normalizeCodeBuddyChatStreamLine(in)
	payload := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(out), []byte("data:")))
	if got := gjson.GetBytes(payload, "object").String(); got != "chat.completion.chunk" {
		t.Fatalf("object = %q, want chat.completion.chunk; line=%s", got, out)
	}
	if got := gjson.GetBytes(payload, "choices.0.delta.content").String(); got != "hi" {
		t.Fatalf("content = %q", got)
	}
}

func TestNormalizeCodeBuddyChatStreamLine_StripsEmptyToolCalls(t *testing.T) {
	t.Parallel()

	in := []byte(`data: {"id":"abc","object":"response","choices":[{"index":0,"delta":{"content":"刚","reasoning_content":"","function_call":null,"tool_calls":[],"extra_fields":null},"finish_reason":""}]}`)
	out := normalizeCodeBuddyChatStreamLine(in)
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

func TestSanitizeCodeBuddyChatPayload_DropsClientMetadata(t *testing.T) {
	t.Parallel()

	in := []byte(`{"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hi"}],"session_id":"s1","chat_id":"c1","features":{"web_search":true},"background_tasks":{"title_generation":true},"id":"owui-1"}`)
	out := prepareCodeBuddyChatPayload(in, "www.codebuddy.ai")
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

func TestNormalizeCodeBuddyChatStreamLine_LeavesDoneAndChunk(t *testing.T) {
	t.Parallel()

	done := []byte("data: [DONE]")
	if got := normalizeCodeBuddyChatStreamLine(done); string(got) != string(done) {
		t.Fatalf("DONE changed: %s", got)
	}
	chunk := []byte(`data: {"object":"chat.completion.chunk","choices":[]}`)
	if got := normalizeCodeBuddyChatStreamLine(chunk); string(got) != string(chunk) {
		t.Fatalf("chunk object changed: %s", got)
	}
}

func TestIsCodeBuddyTransientProviderError(t *testing.T) {
	t.Parallel()

	body := []byte(`{"code":11134,"msg":"the model provider is temporarily unavailable, please retry later or switch to another model"}`)
	if !isCodeBuddyTransientProviderError(http.StatusInternalServerError, body) {
		t.Fatal("expected 11134 to be transient")
	}
	short := []byte(`{"type":"error","code":"11134"}`)
	if !isCodeBuddyTransientProviderError(http.StatusInternalServerError, short) {
		t.Fatal("expected string 11134 to be transient")
	}
	if isCodeBuddyTransientProviderError(http.StatusBadRequest, []byte(`{"code":11128,"msg":"first message is not system prompt"}`)) {
		t.Fatal("11128 is not transient")
	}
}

func TestNormalizeCodeBuddyChatStreamLine_ResponsesTranslatorAcceptsNormalizedChunk(t *testing.T) {
	t.Parallel()

	line := []byte(`data: {"id":"cmb-1","object":"response","created":1,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{"content":"PONG"},"finish_reason":""}]}`)
	var ignored any
	if chunks := responsesconverter.ConvertOpenAIChatCompletionsResponseToOpenAIResponses(t.Context(), "gpt-5.6-luna", nil, nil, line, &ignored); len(chunks) != 0 {
		t.Fatalf("raw object=response should be dropped, got %d chunks", len(chunks))
	}

	var param any
	chunks := responsesconverter.ConvertOpenAIChatCompletionsResponseToOpenAIResponses(t.Context(), "gpt-5.6-luna", nil, nil, normalizeCodeBuddyChatStreamLine(line), &param)
	if len(chunks) == 0 {
		t.Fatal("expected responses SSE after normalizing CodeBuddy object=response")
	}
	joined := string(bytes.Join(chunks, nil))
	if !strings.Contains(joined, "response.created") {
		t.Fatalf("missing response.created in %s", joined)
	}
}

func TestEnsureCodeBuddySystemMessage_LegacyWorkBuddy(t *testing.T) {
	t.Parallel()

	in := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	out := ensureCodeBuddySystemMessage(in, "www.workbuddy.ai")
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "system" {
		t.Fatalf("messages.0.role = %q, want system; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "messages.1.content.0.text").String(); got != "hi" {
		t.Fatalf("user text = %q", got)
	}
}
