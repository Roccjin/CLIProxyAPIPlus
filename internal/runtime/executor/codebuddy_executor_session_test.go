package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodeBuddyPrepareRequest_StickyConversation(t *testing.T) {
	t.Parallel()

	const explicitUUID = "70eba61f-67d5-41a1-aa6a-71f416175d73"
	for _, domain := range []string{codebuddy.DefaultDomainGlobal, codebuddy.DefaultDomain} {
		for _, tt := range []struct {
			name   string
			header string
			seed   string
		}{
			{name: "explicit UUID", header: "X-Conversation-ID", seed: explicitUUID},
			{name: "opaque conversation", header: "x-conversation-id", seed: "client-session"},
			{name: "session header", header: "X-Session-ID", seed: "client-session"},
			{name: "no seed"},
		} {
			t.Run(domain+"/"+tt.name, func(t *testing.T) {
				exec := NewCodeBuddyExecutor(nil)
				var headers []http.Header
				for _, account := range []string{"account-a", "account-a", "account-b"} {
					req, err := http.NewRequest(http.MethodPost, codeBuddyChatURL(domain), nil)
					if err != nil {
						t.Fatal(err)
					}
					if tt.header != "" {
						req.Header[tt.header] = []string{tt.seed}
					}
					auth := &cliproxyauth.Auth{
						ID: account,
						Metadata: map[string]any{
							"access_token": "token-" + account,
							"user_id":      account,
							"domain":       domain,
						},
					}
					if errPrepare := exec.PrepareRequest(req, auth); errPrepare != nil {
						t.Fatal(errPrepare)
					}
					headers = append(headers, req.Header.Clone())
					if tt.seed == explicitUUID && req.Header.Get("X-Conversation-ID") != explicitUUID {
						t.Fatalf("explicit UUID was replaced: %s", req.Header.Get("X-Conversation-ID"))
					}
					if got := req.Header.Get("Authorization"); got != "Bearer token-"+account {
						t.Fatalf("authorization = %q", got)
					}
					if errPrepare := exec.PrepareRequest(req, auth); errPrepare != nil {
						t.Fatal(errPrepare)
					}
					assertCodeBuddyConversationHeaders(t, []http.Header{headers[len(headers)-1], req.Header}, true)
				}
				assertCodeBuddyConversationHeaders(t, headers, tt.seed != "")
			})
		}
	}
}

func TestCodeBuddyExecute_StickyConversation(t *testing.T) {
	t.Parallel()

	const messages = `"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hello"}]`
	for _, tt := range []struct {
		name    string
		opts    cliproxyexecutor.Options
		payload string
		sticky  bool
	}{
		{name: "conversation header", opts: cliproxyexecutor.Options{Headers: http.Header{"X-Conversation-ID": {"client-session"}}}, sticky: true},
		{name: "session header", opts: cliproxyexecutor.Options{Headers: http.Header{"Session_id": {"client-session"}}}, sticky: true},
		{name: "original prompt cache key", opts: cliproxyexecutor.Options{OriginalRequest: []byte(`{` + messages + `,"prompt_cache_key":"client-session"}`)}, sticky: true},
		{name: "payload prompt cache key", payload: `{` + messages + `,"prompt_cache_key":"client-session"}`, sticky: true},
		{name: "execution session", opts: cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: "client-session"}}, sticky: true},
		{name: "derived session", opts: cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.DerivedSessionIDMetadataKey: "ctx:v1:client-session"}}, sticky: true},
		{name: "no seed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var headers []http.Header
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				headers = append(headers, req.Header.Clone())
				body, errRead := io.ReadAll(req.Body)
				if errRead != nil {
					return nil, errRead
				}
				if gjson.GetBytes(body, "prompt_cache_key").Exists() {
					t.Error("session metadata leaked into the sanitized upstream body")
				}
				if len(headers) == 1 {
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"code":11134,"message":"temporarily unavailable"}`)),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(
						"data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5.6-luna\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
					)),
				}, nil
			}))
			payload := tt.payload
			if payload == "" {
				payload = `{` + messages + `}`
			}
			exec := NewCodeBuddyExecutor(nil)
			for _, call := range []struct {
				account string
				stream  bool
				session string
			}{
				{account: "account-a"},
				{account: "account-a", stream: true},
				{account: "account-b"},
				{account: "account-b", stream: true},
				{account: "account-a", session: "another-session"},
			} {
				auth := &cliproxyauth.Auth{
					ID: call.account,
					Metadata: map[string]any{
						"access_token": "token-" + call.account,
						"user_id":      call.account,
						"domain":       codebuddy.DefaultDomainGlobal,
					},
				}
				req := cliproxyexecutor.Request{Model: "gpt-5.6-luna", Payload: []byte(payload)}
				opts := tt.opts
				opts.SourceFormat = sdktranslator.FormatOpenAI
				opts.Stream = call.stream
				if call.session != "" {
					opts.Headers = http.Header{"X-Conversation-ID": {call.session}}
				}
				if call.stream {
					result, errStream := exec.ExecuteStream(ctx, auth, req, opts)
					if errStream != nil {
						t.Fatal(errStream)
					}
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
					}
				} else if _, errExecute := exec.Execute(ctx, auth, req, opts); errExecute != nil {
					t.Fatal(errExecute)
				}
				if got := headers[len(headers)-1].Get("Authorization"); got != "Bearer token-"+call.account {
					t.Fatalf("authorization = %q", got)
				}
			}
			if len(headers) != 6 {
				t.Fatalf("upstream attempts = %d, want 6 including retry", len(headers))
			}
			assertCodeBuddyConversationHeaders(t, headers[:2], true)
			assertCodeBuddyConversationHeaders(t, headers[1:5], tt.sticky)
			assertCodeBuddyConversationHeaders(t, []http.Header{headers[4], headers[5]}, false)
		})
	}
}

func assertCodeBuddyConversationHeaders(t *testing.T, headers []http.Header, sticky bool) {
	t.Helper()

	seen := make(map[string]bool)
	for i, header := range headers {
		conversationValues := 0
		for name, values := range header {
			if strings.EqualFold(name, "X-Conversation-ID") {
				conversationValues += len(values)
			}
		}
		if conversationValues != 1 {
			t.Fatalf("conversation header values = %d, want exactly one", conversationValues)
		}
		conversationID := header.Get("X-Conversation-ID")
		if _, err := uuid.Parse(conversationID); err != nil {
			t.Fatalf("invalid conversation UUID %q: %v", conversationID, err)
		}
		if i > 0 && (conversationID == headers[0].Get("X-Conversation-ID")) != sticky {
			t.Fatalf("sticky=%v: conversation IDs %q and %q", sticky, headers[0].Get("X-Conversation-ID"), conversationID)
		}
		if header.Get("X-Conversation-Request-ID") != header.Get("X-Request-ID") {
			t.Fatal("CodeBuddy conversation request ID must still equal request ID")
		}
		for _, name := range []string{"X-Request-ID", "X-Conversation-Message-ID"} {
			id := header.Get(name)
			if _, err := uuid.Parse(id); err != nil || len(id) != 32 {
				t.Fatalf("invalid %s: %q", name, id)
			}
			if seen[id] {
				t.Fatalf("%s reused per-request ID %q", name, id)
			}
			seen[id] = true
		}
	}
}
