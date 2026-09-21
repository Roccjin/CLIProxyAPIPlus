package executor

import (
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
	ev := codeBuddyChatRequestSendEvent(fp, ids, "hy3", 1000, 2)

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
	if ev["requestModelId"] != "hy3" || ev["mode"] != "unknown" {
		t.Fatalf("model/mode = %v/%v", ev["requestModelId"], ev["mode"])
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
	}, nil, "hy3")
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
			chatBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"model\":\"hy3\",\"object\":\"chat.completion.chunk\",\"created\":1,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"pong\"},\"finish_reason\":\"\"}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"x\",\"model\":\"hy3\",\"object\":\"chat.completion.chunk\",\"created\":1,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":1,\"total_tokens\":9}}\n\n")
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

	if len(reports) != 2 {
		t.Fatalf("report batches = %d, want 2", len(reports))
	}
	if reports[0][0]["eventCode"] != "chat_request_send" || reports[0][1]["eventCode"] != "chat_message_send" {
		t.Fatalf("send events = %#v", reports[0])
	}
	if reports[1][0]["eventCode"] != "chat_message_response" || reports[1][2]["eventCode"] != "chat_request_response" {
		t.Fatalf("response events = %#v", reports[1])
	}
	if reports[0][0]["ideType"] != "CLI" || reports[0][0]["extName"] != codebuddy.CLIExtName {
		t.Fatalf("fingerprint = %#v", reports[0][0])
	}

	if gjson.GetBytes(chatBody, "model").String() != "hy3" {
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
	if chatHeaders.Get("X-Conversation-ID") != reports[0][0]["conversationId"] {
		t.Fatalf("conversation id not correlated")
	}
	if chatHeaders.Get("X-Conversation-Request-ID") != reports[0][0]["requestId"] {
		t.Fatalf("request id not correlated")
	}
}

func TestBuildCodeBuddyActivityChatPayload_UsesHy3(t *testing.T) {
	t.Parallel()

	raw, err := buildCodeBuddyActivityChatPayload("hy3", codebuddy.DefaultDomainGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(raw, "model").String() != "hy3" {
		t.Fatalf("model = %s", raw)
	}
	if !gjson.GetBytes(raw, "stream").Bool() {
		t.Fatal("expected stream")
	}
	if gjson.GetBytes(raw, "max_tokens").Int() != codeBuddyActivityMaxTokens {
		t.Fatalf("max_tokens = %s", raw)
	}
}
