package helps

import (
	"net/http"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

// BuddyFailureKind classifies non-2xx WorkBuddy/CodeBuddy upstream failures.
type BuddyFailureKind string

const (
	BuddyFailureUnknown          BuddyFailureKind = "unknown"
	BuddyFailureTransient        BuddyFailureKind = "transient"
	BuddyFailureGatewayTimeout   BuddyFailureKind = "gateway_timeout"
	BuddyFailureCreditsExhausted BuddyFailureKind = "credits_exhausted"
)

// BuddyFailure describes a classified Buddy upstream failure. UpstreamCode is
// the normalized structured business code when one was recognized.
type BuddyFailure struct {
	Kind         BuddyFailureKind
	UpstreamCode string
}

const (
	buddyCreditsExhaustedCode = "14018"
	buddyTransientCode        = "11134"
)

// buddyBusinessCodePaths lists structured business code locations in priority
// order; the first present value wins.
var buddyBusinessCodePaths = []string{
	"error.data.code",
	"error.code",
	"data.code",
	"code",
}

// ClassifyBuddyFailure categorizes a Buddy upstream non-2xx response. The
// classification order is fixed: structured credits-exhausted code first, then
// HTTP gateway timeout, then the existing transient provider patterns, then
// unknown. Text, HTML, and purchase links alone never produce
// BuddyFailureCreditsExhausted.
func ClassifyBuddyFailure(status int, body []byte) BuddyFailure {
	if gjson.ValidBytes(body) {
		if code := buddyBusinessCode(body); code == buddyCreditsExhaustedCode {
			return BuddyFailure{Kind: BuddyFailureCreditsExhausted, UpstreamCode: code}
		}
	}
	if status == http.StatusGatewayTimeout {
		return BuddyFailure{Kind: BuddyFailureGatewayTimeout}
	}
	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		if !gjson.ValidBytes(body) {
			break
		}
		if code := buddyBusinessCode(body); code == buddyTransientCode {
			return BuddyFailure{Kind: BuddyFailureTransient, UpstreamCode: code}
		}
		msg := strings.ToLower(strings.Join([]string{
			gjson.GetBytes(body, "code").String(),
			gjson.GetBytes(body, "msg").String(),
			gjson.GetBytes(body, "message").String(),
			gjson.GetBytes(body, "extError.code").String(),
			gjson.GetBytes(body, "extError.message").String(),
		}, " "))
		if strings.Contains(msg, "temporarily unavailable") || strings.Contains(msg, "service_unavailable") {
			return BuddyFailure{Kind: BuddyFailureTransient}
		}
	}
	return BuddyFailure{Kind: BuddyFailureUnknown}
}

// buddyBusinessCode returns the normalized business code from the first
// whitelisted JSON path that exists. Numeric and string values are both
// normalized to a trimmed string.
func buddyBusinessCode(body []byte) string {
	for _, path := range buddyBusinessCodePaths {
		result := gjson.GetBytes(body, path)
		if !result.Exists() {
			continue
		}
		return strings.TrimSpace(result.String())
	}
	return ""
}

// NewBuddyFailureError converts a classified failure into a normalized
// *auth.Error with a safe, non-sensitive message. It returns nil for kinds
// that keep the existing generic error behavior. Raw response bodies, purchase
// links, and upstream request IDs are never copied into the error.
func NewBuddyFailureError(provider string, failure BuddyFailure) *cliproxyauth.Error {
	label := buddyProviderLabel(provider)
	switch failure.Kind {
	case BuddyFailureGatewayTimeout:
		return &cliproxyauth.Error{
			Code:       cliproxyauth.ErrorCodeUpstreamGatewayTimeout,
			Message:    label + " upstream gateway timed out",
			Retryable:  true,
			HTTPStatus: http.StatusGatewayTimeout,
		}
	case BuddyFailureCreditsExhausted:
		return &cliproxyauth.Error{
			Code:       cliproxyauth.ErrorCodeCredentialCreditsExhausted,
			Message:    label + " credits exhausted",
			Retryable:  false,
			HTTPStatus: http.StatusTooManyRequests,
		}
	default:
		return nil
	}
}

// buddyProviderLabel maps known provider keys to their display names and
// returns the trimmed provider otherwise.
func buddyProviderLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "workbuddy":
		return "WorkBuddy"
	case "codebuddy":
		return "CodeBuddy"
	default:
		return strings.TrimSpace(provider)
	}
}
