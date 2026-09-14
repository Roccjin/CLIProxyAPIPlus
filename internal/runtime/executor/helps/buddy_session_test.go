package helps

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestBuddyConversationUUID(t *testing.T) {
	t.Parallel()

	codeBuddy := uuid.NewSHA1(uuid.NameSpaceURL, []byte("www.codebuddy.ai/conversation"))
	workBuddy := uuid.NewSHA1(uuid.NameSpaceURL, []byte("www.workbuddy.ai/conversation"))
	first := BuddyConversationUUID(codeBuddy, "client-session")
	if _, err := uuid.Parse(first); err != nil {
		t.Fatalf("invalid conversation UUID: %v", err)
	}
	if got := BuddyConversationUUID(codeBuddy, " client-session "); got != first {
		t.Fatalf("same seed produced %s and %s", first, got)
	}
	if got := BuddyConversationUUID(codeBuddy, "another-session"); got == first {
		t.Fatal("different sessions must have different conversation IDs")
	}
	if got := BuddyConversationUUID(workBuddy, "client-session"); got == first {
		t.Fatal("opaque seeds must be scoped to the provider")
	}
	const explicit = "70eba61f-67d5-41a1-aa6a-71f416175d73"
	if got := BuddyConversationUUID(codeBuddy, " "+strings.ToUpper(explicit)+" "); got != explicit {
		t.Fatalf("explicit UUID = %s, want %s", got, explicit)
	}
	if got := BuddyConversationUUID(codeBuddy, first); got != first {
		t.Fatalf("generated UUID was rehashed: %s", got)
	}
	if BuddyConversationUUID(codeBuddy, "") == BuddyConversationUUID(codeBuddy, " ") {
		t.Fatal("unrelated requests without a seed must get fresh conversation IDs")
	}
}

func TestResolveBuddyConversationID_Priority(t *testing.T) {
	t.Parallel()

	opts := cliproxyexecutor.Options{
		Headers: http.Header{
			"x-conversation-id":        {"conversation-header"},
			"x-session-id":             {"session-header"},
			"X-Claude-Code-Session-Id": {"claude-header"},
		},
		OriginalRequest: []byte(`{"prompt_cache_key":"original-cache-key"}`),
		Metadata: map[string]any{
			cliproxyexecutor.ExecutionSessionMetadataKey: "execution-session",
			cliproxyexecutor.DerivedSessionIDMetadataKey: "ctx:v1:derived",
		},
	}
	payload := []byte(`{"session_id":"payload-session"}`)
	check := func(want string) {
		t.Helper()
		if got := ResolveBuddyConversationID(opts, payload); got != want {
			t.Fatalf("conversation seed = %q, want %q", got, want)
		}
	}
	check("conversation-header")
	delete(opts.Headers, "x-conversation-id")
	check("session-header")
	delete(opts.Headers, "x-session-id")
	check("claude-header")
	opts.Headers = nil
	check("original-cache-key")
	opts.OriginalRequest = nil
	check("payload-session")
	payload = nil
	check("execution-session")
	delete(opts.Metadata, cliproxyexecutor.ExecutionSessionMetadataKey)
	check("ctx:v1:derived")
	opts.Metadata = nil
	check("")
}

func TestResolveBuddyConversationID_ExplicitForms(t *testing.T) {
	t.Parallel()

	for _, header := range []string{"X-Conversation-ID", "X-Session-ID", "Session-Id", "Session_id", "X-Claude-Code-Session-Id", "X-Session-Affinity", "X-Client-Request-Id"} {
		t.Run(header, func(t *testing.T) {
			opts := cliproxyexecutor.Options{Headers: http.Header{
				strings.ToLower(header): {"", "bad\nsession", strings.Repeat("x", 257), " valid-session "},
			}}
			if got := ResolveBuddyConversationID(opts, nil); got != "valid-session" {
				t.Fatalf("header seed = %q", got)
			}
		})
	}
	for _, payload := range []string{
		`{"session_id":"client-session"}`,
		`{"sessionId":"client-session"}`,
		`{"conversation_id":"client-session"}`,
		`{"prompt_cache_key":"client-session"}`,
		`{"metadata":{"user_id":"client-session"}}`,
		`{"metadata":{"user_id":"{\"session_id\":\"client-session\"}"}}`,
		`{"conversation":{"id":"client-session"}}`,
		`{"conversation":"client-session"}`,
	} {
		if got := ResolveBuddyConversationID(cliproxyexecutor.Options{}, []byte(payload)); got != "client-session" {
			t.Errorf("payload %s: seed = %q", payload, got)
		}
	}
	const explicit = "70eba61f-67d5-41a1-aa6a-71f416175d73"
	if got := ResolveBuddyConversationID(cliproxyexecutor.Options{}, []byte(`{"metadata":{"user_id":"user_session_`+explicit+`"}}`)); got != explicit {
		t.Fatalf("Claude metadata session = %q", got)
	}
}

func TestResolveBuddyConversationID_InvalidSeedsFallThrough(t *testing.T) {
	t.Parallel()

	for _, invalid := range []any{"", " ", "bad\nsession", "\tsession", strings.Repeat("x", 257), 123} {
		opts := cliproxyexecutor.Options{
			Headers:         http.Header{"X-Conversation-ID": {"bad\nsession"}},
			OriginalRequest: []byte(`{"prompt_cache_key":"invalid\nsession"}`),
			Metadata: map[string]any{
				cliproxyexecutor.ExecutionSessionMetadataKey: invalid,
				cliproxyexecutor.DerivedSessionIDMetadataKey: "ctx:v1:valid",
			},
		}
		if got := ResolveBuddyConversationID(opts, nil); got != "ctx:v1:valid" {
			t.Errorf("invalid seed %q: fallback = %q", invalid, got)
		}
		opts.Metadata[cliproxyexecutor.DerivedSessionIDMetadataKey] = invalid
		if got := ResolveBuddyConversationID(opts, nil); got != "" {
			t.Errorf("invalid seed %q was accepted: %q", invalid, got)
		}
	}
}
