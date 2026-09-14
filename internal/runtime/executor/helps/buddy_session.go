package helps

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	cliproxysession "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/session"
)

// BuddyConversationUUID preserves explicit UUIDs and maps opaque session seeds to
// a provider-scoped UUID without depending on the selected upstream credential.
func BuddyConversationUUID(namespace uuid.UUID, seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return uuid.NewString()
	}
	if parsed, err := uuid.Parse(seed); err == nil {
		return parsed.String()
	}
	return uuid.NewSHA1(namespace, []byte(seed)).String()
}

// ResolveBuddyConversationID selects the conversation seed before request
// translation and sanitization can remove client session metadata.
func ResolveBuddyConversationID(opts cliproxyexecutor.Options, payload []byte) string {
	if id := BuddyHeaderConversationID(opts.Headers); id != "" {
		return id
	}
	if id := cliproxysession.ExplicitID(opts.Headers, opts.OriginalRequest); id != "" {
		return id
	}
	if id := cliproxysession.ExplicitID(nil, payload); id != "" {
		return id
	}
	for _, key := range []string{cliproxyexecutor.ExecutionSessionMetadataKey, cliproxyexecutor.DerivedSessionIDMetadataKey} {
		raw, _ := opts.Metadata[key].(string)
		if id := cliproxysession.NormalizeExplicitID(raw); id != "" {
			return id
		}
	}
	return ""
}

// BuddyHeaderConversationID keeps the same explicit-header priority for
// CodeBuddy and WorkBuddy, including non-canonical HTTP header keys.
func BuddyHeaderConversationID(headers http.Header) string {
	for _, name := range []string{
		"X-Conversation-ID",
		"X-Session-ID",
		"Session-Id",
		"Session_id",
		"X-Claude-Code-Session-Id",
		"X-Session-Affinity",
		"X-Client-Request-Id",
	} {
		for key, values := range headers {
			if !strings.EqualFold(key, name) {
				continue
			}
			for _, value := range values {
				if id := cliproxysession.NormalizeExplicitID(value); id != "" {
					return id
				}
			}
		}
		if id := cliproxysession.NormalizeExplicitID(headers.Get(name)); id != "" {
			return id
		}
	}
	return ""
}
