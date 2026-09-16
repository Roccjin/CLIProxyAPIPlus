package helps

import (
	"net/http"
	"testing"
)

func TestClassifyBuddyFailure(t *testing.T) {
	t.Parallel()

	const apisiHTML = `<html><head><title>504 Gateway Time-out</title></head><body>openresty APISIX</body></html>`

	tests := []struct {
		name         string
		status       int
		body         string
		wantKind     BuddyFailureKind
		wantUpstream string
	}{
		{
			name:         "error.data.code number credits",
			status:       http.StatusBadRequest,
			body:         `{"error":{"data":{"code":14018,"msg":"Credits exhausted","requestId":"abc"}}}`,
			wantKind:     BuddyFailureCreditsExhausted,
			wantUpstream: "14018",
		},
		{
			name:         "error.data.code string credits",
			status:       http.StatusInternalServerError,
			body:         `{"error":{"data":{"code":"14018"}}}`,
			wantKind:     BuddyFailureCreditsExhausted,
			wantUpstream: "14018",
		},
		{
			name:         "error.code credits",
			status:       http.StatusBadRequest,
			body:         `{"error":{"code":14018}}`,
			wantKind:     BuddyFailureCreditsExhausted,
			wantUpstream: "14018",
		},
		{
			name:         "data.code credits",
			status:       http.StatusBadRequest,
			body:         `{"data":{"code":"14018"}}`,
			wantKind:     BuddyFailureCreditsExhausted,
			wantUpstream: "14018",
		},
		{
			name:         "top level code credits",
			status:       http.StatusBadRequest,
			body:         `{"code":14018,"msg":"Credits exhausted"}`,
			wantKind:     BuddyFailureCreditsExhausted,
			wantUpstream: "14018",
		},
		{
			name:     "gateway timeout apisix html",
			status:   http.StatusGatewayTimeout,
			body:     apisiHTML,
			wantKind: BuddyFailureGatewayTimeout,
		},
		{
			name:     "gateway timeout html containing 14018 stays gateway",
			status:   http.StatusGatewayTimeout,
			body:     `<html><body>error 14018</body></html>`,
			wantKind: BuddyFailureGatewayTimeout,
		},
		{
			name:     "gateway timeout plain text",
			status:   http.StatusGatewayTimeout,
			body:     `upstream timed out`,
			wantKind: BuddyFailureGatewayTimeout,
		},
		{
			name:         "gateway timeout with structured credits wins credits",
			status:       http.StatusGatewayTimeout,
			body:         `{"error":{"data":{"code":14018}}}`,
			wantKind:     BuddyFailureCreditsExhausted,
			wantUpstream: "14018",
		},
		{
			name:     "credits text only is unknown",
			status:   http.StatusBadRequest,
			body:     `{"msg":"Credits exhausted. Please visit https://example.com/purchase"}`,
			wantKind: BuddyFailureUnknown,
		},
		{
			name:     "purchase link only is unknown",
			status:   http.StatusPaymentRequired,
			body:     `{"msg":"please visit https://example.com/purchase"}`,
			wantKind: BuddyFailureUnknown,
		},
		{
			name:     "invalid json is unknown",
			status:   http.StatusBadRequest,
			body:     `not json at all {{{`,
			wantKind: BuddyFailureUnknown,
		},
		{
			name:     "invalid json gateway timeout is gateway",
			status:   http.StatusGatewayTimeout,
			body:     `not json at all {{{`,
			wantKind: BuddyFailureGatewayTimeout,
		},
		{
			name:     "html containing 14018 at 500 is unknown",
			status:   http.StatusInternalServerError,
			body:     `<html><body>14018 credits exhausted</body></html>`,
			wantKind: BuddyFailureUnknown,
		},
		{
			name:     "unrelated code is unknown",
			status:   http.StatusBadRequest,
			body:     `{"code":11128,"msg":"first message is not system prompt"}`,
			wantKind: BuddyFailureUnknown,
		},
		{
			name:         "internal error numeric 11134 is transient",
			status:       http.StatusInternalServerError,
			body:         `{"code":11134,"msg":"temporarily unavailable"}`,
			wantKind:     BuddyFailureTransient,
			wantUpstream: "11134",
		},
		{
			name:         "bad gateway string 11134 is transient",
			status:       http.StatusBadGateway,
			body:         `{"code":"11134"}`,
			wantKind:     BuddyFailureTransient,
			wantUpstream: "11134",
		},
		{
			name:     "unavailable message is transient",
			status:   http.StatusServiceUnavailable,
			body:     `{"msg":"service temporarily unavailable"}`,
			wantKind: BuddyFailureTransient,
		},
		{
			name:     "extError service_unavailable is transient",
			status:   http.StatusServiceUnavailable,
			body:     `{"extError":{"code":"service_unavailable"}}`,
			wantKind: BuddyFailureTransient,
		},
		{
			name:     "400 with 11134 is not transient",
			status:   http.StatusBadRequest,
			body:     `{"code":11134}`,
			wantKind: BuddyFailureUnknown,
		},
		{
			name:     "504 with 11134 is gateway not transient",
			status:   http.StatusGatewayTimeout,
			body:     `{"code":11134}`,
			wantKind: BuddyFailureGatewayTimeout,
		},
		{
			name:     "plain text unavailable body is unknown",
			status:   http.StatusServiceUnavailable,
			body:     `service temporarily unavailable`,
			wantKind: BuddyFailureUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyBuddyFailure(tc.status, []byte(tc.body))
			if got.Kind != tc.wantKind {
				t.Fatalf("Kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.UpstreamCode != tc.wantUpstream {
				t.Fatalf("UpstreamCode = %q, want %q", got.UpstreamCode, tc.wantUpstream)
			}
		})
	}
}

func TestNewBuddyFailureError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		provider       string
		failure        BuddyFailure
		wantNil        bool
		wantCode       string
		wantMessage    string
		wantRetryable  bool
		wantHTTPStatus int
	}{
		{
			name:           "workbuddy gateway timeout",
			provider:       "workbuddy",
			failure:        BuddyFailure{Kind: BuddyFailureGatewayTimeout},
			wantCode:       "upstream_gateway_timeout",
			wantMessage:    "WorkBuddy upstream gateway timed out",
			wantRetryable:  true,
			wantHTTPStatus: http.StatusGatewayTimeout,
		},
		{
			name:           "codebuddy gateway timeout",
			provider:       "codebuddy",
			failure:        BuddyFailure{Kind: BuddyFailureGatewayTimeout},
			wantCode:       "upstream_gateway_timeout",
			wantMessage:    "CodeBuddy upstream gateway timed out",
			wantRetryable:  true,
			wantHTTPStatus: http.StatusGatewayTimeout,
		},
		{
			name:           "workbuddy credits exhausted",
			provider:       "workbuddy",
			failure:        BuddyFailure{Kind: BuddyFailureCreditsExhausted, UpstreamCode: "14018"},
			wantCode:       "credential_credits_exhausted",
			wantMessage:    "WorkBuddy credits exhausted",
			wantRetryable:  false,
			wantHTTPStatus: http.StatusTooManyRequests,
		},
		{
			name:           "codebuddy credits exhausted",
			provider:       "codebuddy",
			failure:        BuddyFailure{Kind: BuddyFailureCreditsExhausted, UpstreamCode: "14018"},
			wantCode:       "credential_credits_exhausted",
			wantMessage:    "CodeBuddy credits exhausted",
			wantRetryable:  false,
			wantHTTPStatus: http.StatusTooManyRequests,
		},
		{
			name:           "unknown provider passes through trimmed",
			provider:       "  qoder  ",
			failure:        BuddyFailure{Kind: BuddyFailureGatewayTimeout},
			wantCode:       "upstream_gateway_timeout",
			wantMessage:    "qoder upstream gateway timed out",
			wantRetryable:  true,
			wantHTTPStatus: http.StatusGatewayTimeout,
		},
		{
			name:     "unknown kind returns nil",
			provider: "workbuddy",
			failure:  BuddyFailure{Kind: BuddyFailureUnknown},
			wantNil:  true,
		},
		{
			name:     "transient kind returns nil",
			provider: "workbuddy",
			failure:  BuddyFailure{Kind: BuddyFailureTransient, UpstreamCode: "11134"},
			wantNil:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NewBuddyFailureError(tc.provider, tc.failure)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("NewBuddyFailureError = %#v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("NewBuddyFailureError = nil, want error")
			}
			if got.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", got.Code, tc.wantCode)
			}
			if got.Message != tc.wantMessage {
				t.Errorf("Message = %q, want %q", got.Message, tc.wantMessage)
			}
			if got.Retryable != tc.wantRetryable {
				t.Errorf("Retryable = %v, want %v", got.Retryable, tc.wantRetryable)
			}
			if got.HTTPStatus != tc.wantHTTPStatus {
				t.Errorf("HTTPStatus = %d, want %d", got.HTTPStatus, tc.wantHTTPStatus)
			}
		})
	}
}
