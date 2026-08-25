package qoder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestMaskPAT(t *testing.T) {
	got := MaskPAT("pt-abcdefghijk")
	if !strings.HasPrefix(got, "pt-") || !strings.Contains(got, "****") {
		t.Fatalf("MaskPAT = %q, want masked pt- token", got)
	}
	if strings.Contains(got, "abcdefgh") {
		t.Fatalf("MaskPAT leaked PAT body: %q", got)
	}
}

func TestIsPAT(t *testing.T) {
	if (&QoderTokenStorage{Token: "dt-abc"}).IsPAT() {
		t.Fatal("oauth storage reported as PAT")
	}
	if !(&QoderTokenStorage{AuthMode: AuthModePAT, PersonalToken: "pt-abc"}).IsPAT() {
		t.Fatal("auth_mode=pat should be PAT")
	}
	if !(&QoderTokenStorage{PersonalToken: "pt-abc"}).IsPAT() {
		t.Fatal("personal_token should imply PAT")
	}
}

func TestPATAuthFileName(t *testing.T) {
	got := PATAuthFileName("user@example.com", "pt-xxxxYYYY")
	if got != "qoder-user@example.com-pat-YYYY.json" {
		t.Fatalf("PATAuthFileName = %q", got)
	}
}

func TestParseJobTokenSession(t *testing.T) {
	body := []byte(`{
		"id": "user-1",
		"name": "Ada",
		"email": "ada@example.com",
		"securityOauthToken": "jt-session",
		"refreshToken": "rt-1",
		"expireTime": 2000000000000
	}`)
	sess, err := parseJobTokenSession(body)
	if err != nil {
		t.Fatal(err)
	}
	if sess.SecurityOauthToken != "jt-session" || sess.UserID != "user-1" || sess.Email != "ada@example.com" {
		t.Fatalf("parsed %+v", sess)
	}
}

func TestExchangeJobToken_RequestShape(t *testing.T) {
	var captured struct {
		method string
		url    string
		date   string
		sig    string
		app    string
		body   []byte
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.url = r.URL.String()
		captured.date = r.Header.Get("date")
		captured.sig = r.Header.Get("signature")
		captured.app = r.Header.Get("appcode")
		captured.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"uid-1","name":"N","email":"n@e.com","securityOauthToken":"jt-ok","refreshToken":"rt-ok","expireTime":2000000000000}`))
	}))
	defer server.Close()

	auth := NewQoderAuth(&config.Config{})
	auth.jobTokenURL = server.URL
	sess, err := auth.ExchangeJobToken(context.Background(), "pt-testtoken", "mid", "mid", "5")
	if err != nil {
		t.Fatal(err)
	}
	if sess.SecurityOauthToken != "jt-ok" {
		t.Fatalf("token = %q", sess.SecurityOauthToken)
	}
	if captured.method != http.MethodPost {
		t.Fatalf("method = %s", captured.method)
	}
	if captured.app != qoderAppCode {
		t.Fatalf("appcode = %q", captured.app)
	}
	if captured.sig != jobTokenSignature(captured.date) {
		t.Fatalf("signature mismatch")
	}
	if json.Valid(captured.body) {
		t.Fatal("jobToken body should be encoded, not raw JSON")
	}
}

func TestLoginWithPAT_RejectsNonPT(t *testing.T) {
	auth := NewQoderAuth(&config.Config{})
	_, err := auth.LoginWithPAT(context.Background(), "not-a-pat")
	if err == nil || !strings.Contains(err.Error(), "pt-") {
		t.Fatalf("error = %v, want pt- prefix error", err)
	}
}

func TestRefreshTokenIfNeeded_PATExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"uid-1","securityOauthToken":"jt-new","refreshToken":"rt-new","expireTime":9999999999999}`))
	}))
	defer server.Close()

	orig := jobTokenEndpoint
	jobTokenEndpoint = server.URL
	t.Cleanup(func() { jobTokenEndpoint = orig })

	storage := &QoderTokenStorage{
		AuthMode:      AuthModePAT,
		PersonalToken: "pt-testtoken",
		Token:         "jt-old",
		ExpireTime:    time.Now().UnixMilli() - 1000,
		MachineID:     "mid",
		MachineToken:  "mid",
		MachineType:   "5",
	}
	if err := RefreshTokenIfNeeded(context.Background(), &config.Config{}, storage, 600, ""); err != nil {
		t.Fatal(err)
	}
	if storage.Token != "jt-new" {
		t.Fatalf("token after refresh = %q", storage.Token)
	}
}

func TestRefreshTokenIfNeeded_OAuthSkipsWhenNotPAT(t *testing.T) {
	storage := &QoderTokenStorage{
		Token:      "dt-old",
		ExpireTime: 0,
	}
	if err := RefreshTokenIfNeeded(context.Background(), &config.Config{}, storage, 600, ""); err != nil {
		t.Fatalf("oauth with expire_time=0 should no-op: %v", err)
	}
}
