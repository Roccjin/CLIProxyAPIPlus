package executor

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

func TestCodeBuddyChatRequestSendEvent_CLIFingerprint(t *testing.T) {
	t.Parallel()

	ids := codeBuddyChatIDs{
		ConversationID:        "conv-1",
		ConversationRequestID: "req1",
		MessageID:             "msg1",
		TraceID:               "trace1",
	}
	fp := codeBuddyCLIFingerprint("user-1", "nick@example.com", "machine-1", "session-1")
	ev := codeBuddyChatRequestSendEvent(fp, ids, "deepseek-v4.1-flash", 1000, 2)

	if ev["eventCode"] != "chat_request_send" {
		t.Fatalf("eventCode = %v", ev["eventCode"])
	}
	if ev["ideType"] != "CLI" || ev["ideName"] != "CLI" {
		t.Fatalf("ide = %v/%v", ev["ideName"], ev["ideType"])
	}
	if ev["extName"] != codebuddy.CLIExtName {
		t.Fatalf("extName = %v", ev["extName"])
	}
	if ev["agentName"] != "cli" || ev["agentType"] != "main" {
		t.Fatalf("agent = %v/%v", ev["agentName"], ev["agentType"])
	}
	if ev["machineId"] != "machine-1" || ev["userId"] != "user-1" {
		t.Fatalf("ids = %#v", ev)
	}
	if ev["requestModelId"] != "deepseek-v4.1-flash" || ev["mode"] != "unknown" {
		t.Fatalf("model/mode = %v/%v", ev["requestModelId"], ev["mode"])
	}
	if ev["vcsType"] != "unknown" {
		t.Fatalf("vcsType = %v", ev["vcsType"])
	}
	if ev["requestId"] != "req1" || ev["conversationId"] != "conv-1" {
		t.Fatalf("correlation = %#v", ev)
	}
}

func TestPingCodeBuddyDailyActivity_InternationalOnly(t *testing.T) {
	t.Parallel()

	_, err := PingCodeBuddyDailyActivity(context.Background(), &cliproxyauth.Auth{
		Metadata: map[string]any{
			"access_token": "token",
			"user_id":      "user-1",
			"domain":       codebuddy.DefaultDomain,
		},
	}, nil, "deepseek-v4.1-flash")
	if err == nil || !strings.Contains(err.Error(), "international-only") {
		t.Fatalf("err = %v", err)
	}
}

func TestPingCodeBuddyDailyActivityFromBase_ReportThenChat(t *testing.T) {
	t.Parallel()

	var reports [][]map[string]any
	var chatBody []byte
	var chatHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case codeBuddyReportPath:
			if r.Header.Get("User-Agent") != codebuddy.UserAgent {
				t.Errorf("report UA = %s", r.Header.Get("User-Agent"))
			}
			if r.Header.Get("X-Domain") != codebuddy.DefaultDomainGlobal {
				t.Errorf("report X-Domain = %s", r.Header.Get("X-Domain"))
			}
			raw, _ := io.ReadAll(r.Body)
			var events []map[string]any
			if err := json.Unmarshal(raw, &events); err != nil {
				t.Errorf("report json: %v", err)
			}
			reports = append(reports, events)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "OK"})
		case codeBuddyChatPath:
			chatHeaders = r.Header.Clone()
			rawBody, _ := io.ReadAll(r.Body)
			if r.Header.Get("Content-Encoding") == "gzip" {
				zr, err := gzip.NewReader(bytes.NewReader(rawBody))
				if err != nil {
					t.Errorf("gzip: %v", err)
				} else {
					chatBody, _ = io.ReadAll(zr)
					_ = zr.Close()
				}
			} else {
				chatBody = rawBody
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"model\":\"deepseek-v4.1-flash\",\"object\":\"chat.completion.chunk\",\"created\":1,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"pong\"},\"finish_reason\":\"\"}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"model\":\"deepseek-v4.1-flash\",\"object\":\"chat.completion.chunk\",\"created\":1,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":1,\"total_tokens\":9}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	auth := &cliproxyauth.Auth{Metadata: map[string]any{
		"access_token": "token",
		"user_id":      "user-1",
		"email":        "nick@example.com",
		"domain":       codebuddy.DefaultDomainGlobal,
	}}
	result, err := pingCodeBuddyDailyActivity(context.Background(), auth, nil, srv.URL, "hy3")
	if err != nil {
		t.Fatal(err)
	}
	if result.MachineID == "" || result.FinishReason != "stop" {
		t.Fatalf("result = %+v", result)
	}
	if result.TotalTokens != 9 {
		t.Fatalf("tokens = %+v", result)
	}
	if got := auth.Metadata[cliproxyauth.MetadataKeyCLIMachineID]; got != result.MachineID {
		t.Fatalf("persisted machine id = %#v", got)
	}

	if len(reports) != 5 {
		t.Fatalf("report batches = %d, want 5", len(reports))
	}
	if reports[0][0]["eventCode"] != "plugin_status" || reports[0][0]["text"] != "pluginStart" {
		t.Fatalf("plugin event = %#v", reports[0])
	}
	if _, ok := reports[0][0]["agentName"]; ok {
		t.Fatalf("plugin event includes agentName: %#v", reports[0][0])
	}
	if reports[1][0]["eventCode"] != "user_auth_action" || reports[1][0]["action"] != "login" {
		t.Fatalf("login event = %#v", reports[1])
	}
	if reports[2][0]["eventCode"] != "chat_request_send" || reports[2][1]["eventCode"] != "chat_message_send" {
		t.Fatalf("send events = %#v", reports[2])
	}
	if reports[3][0]["eventCode"] != "chat_message_response" || reports[3][1]["eventCode"] != "chat_message_status" {
		t.Fatalf("message response events = %#v", reports[3])
	}
	if reports[4][0]["eventCode"] != "chat_request_response" {
		t.Fatalf("request response = %#v", reports[4])
	}
	if reports[2][0]["ideType"] != "CLI" || reports[2][0]["extName"] != codebuddy.CLIExtName {
		t.Fatalf("fingerprint = %#v", reports[2][0])
	}
	if reports[2][0]["os"] != "win32" || reports[2][0]["arch"] != "x64" || reports[2][0]["vcsType"] != "unknown" {
		t.Fatalf("cli runtime = %#v", reports[2][0])
	}
	if reports[2][0]["requestModelId"] != "deepseek-v4.1-flash" || reports[2][0]["requestModelName"] != "Deepseek-V4.1-Flash" {
		t.Fatalf("reported model = %v/%v", reports[2][0]["requestModelId"], reports[2][0]["requestModelName"])
	}
	if reports[3][0]["firstTokenAt"] == nil {
		t.Fatal("missing firstTokenAt")
	}

	if gjson.GetBytes(chatBody, "model").String() != "deepseek-v4.1-flash" {
		t.Fatalf("chat model = %s", chatBody)
	}
	if gjson.GetBytes(chatBody, "messages.0.role").String() != "system" {
		t.Fatalf("first role = %s", chatBody)
	}
	if chatHeaders.Get("X-Request-ID") != chatHeaders.Get("X-Conversation-Message-ID") {
		t.Fatalf("request/message id mismatch: %s / %s", chatHeaders.Get("X-Request-ID"), chatHeaders.Get("X-Conversation-Message-ID"))
	}
	if chatHeaders.Get("X-Root-Request-ID") != chatHeaders.Get("X-Conversation-Request-ID") {
		t.Fatalf("root/request id mismatch")
	}
	if chatHeaders.Get("X-Conversation-ID") != reports[2][0]["conversationId"] {
		t.Fatalf("conversation id not correlated")
	}
	if chatHeaders.Get("X-Conversation-Request-ID") != reports[2][0]["requestId"] {
		t.Fatalf("request id not correlated")
	}
	if chatHeaders.Get("X-Agent-Purpose") != "conversation" {
		t.Fatalf("purpose = %s", chatHeaders.Get("X-Agent-Purpose"))
	}
	if chatHeaders.Get("x-stainless-runtime") != "node" || chatHeaders.Get("x-stainless-os") != "Windows" {
		t.Fatalf("stainless = %v", chatHeaders)
	}
	if !strings.HasPrefix(chatHeaders.Get("traceparent"), "00-") {
		t.Fatalf("traceparent = %s", chatHeaders.Get("traceparent"))
	}
	if gjson.GetBytes(chatBody, "max_tokens").Int() != 128000 || gjson.GetBytes(chatBody, "temperature").Int() != 1 {
		t.Fatalf("chat sampling = %s", chatBody)
	}
	if gjson.GetBytes(chatBody, "reasoning_effort").String() != "xhigh" {
		t.Fatalf("reasoning_effort = %s", chatBody)
	}
	if gjson.GetBytes(chatBody, "tools.#").Int() != 24 {
		t.Fatalf("tools = %s", gjson.GetBytes(chatBody, "tools.#").Raw)
	}
	if !strings.HasPrefix(gjson.GetBytes(chatBody, "messages.0.content").String(), "You are CodeBuddy Code.") {
		t.Fatal("system prompt is not the CLI prompt")
	}
	if gjson.GetBytes(chatBody, "messages.1.content.2.text").String() != "<user_query>你好</user_query>" {
		t.Fatalf("user query = %s", chatBody)
	}
	if strings.Contains(string(chatBody), "29781") {
		t.Fatal("activity payload contains a personal path")
	}
}

func TestBuildCodeBuddyActivityChatPayload_UsesCLIConversation(t *testing.T) {
	t.Parallel()

	raw, inputLength, err := buildCodeBuddyActivityChatPayload("hy3", codebuddy.DefaultDomainGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(raw, "model").String() != "deepseek-v4.1-flash" {
		t.Fatalf("model = %s", raw)
	}
	if !gjson.GetBytes(raw, "stream").Bool() || !gjson.GetBytes(raw, "stream_options.include_usage").Bool() {
		t.Fatal("expected streaming usage")
	}
	if gjson.GetBytes(raw, "max_tokens").Int() != codeBuddyActivityMaxTokens {
		t.Fatalf("max_tokens = %s", raw)
	}
	if inputLength < 1000 {
		t.Fatalf("inputLength = %d", inputLength)
	}
	if gjson.GetBytes(raw, "messages.1.content.2.text").String() != "<user_query>你好</user_query>" {
		t.Fatalf("query missing: %s", gjson.GetBytes(raw, "messages.1.content").Raw)
	}
}
