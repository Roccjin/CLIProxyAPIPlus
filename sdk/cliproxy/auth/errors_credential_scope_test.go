package auth

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorIsCredentialScoped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *Error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "credits exhausted", err: &Error{Code: ErrorCodeCredentialCreditsExhausted}, want: true},
		{name: "gateway timeout", err: &Error{Code: ErrorCodeUpstreamGatewayTimeout}, want: false},
		{name: "request scoped", err: &Error{Code: ErrorCodeRequestScoped}, want: false},
		{name: "other code", err: &Error{Code: "auth_not_found"}, want: false},
		{name: "empty code", err: &Error{Message: "boom"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.IsCredentialScoped(); got != tc.want {
				t.Fatalf("IsCredentialScoped() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsCredentialScopedErrorRecognizesCreditsExhausted(t *testing.T) {
	t.Parallel()

	if !isCredentialScopedError(&Error{Code: ErrorCodeCredentialCreditsExhausted}) {
		t.Fatal("isCredentialScopedError did not recognize credential_credits_exhausted")
	}
	wrapped := fmt.Errorf("outer: %w", &Error{Code: ErrorCodeCredentialCreditsExhausted})
	if !isCredentialScopedError(wrapped) {
		t.Fatal("isCredentialScopedError did not recognize wrapped credential_credits_exhausted")
	}
	if isCredentialScopedError(&Error{Code: ErrorCodeUpstreamGatewayTimeout}) {
		t.Fatal("isCredentialScopedError treated upstream_gateway_timeout as credential scoped")
	}
	if isCredentialScopedError(errors.New("plain")) {
		t.Fatal("isCredentialScopedError treated plain error as credential scoped")
	}
}
