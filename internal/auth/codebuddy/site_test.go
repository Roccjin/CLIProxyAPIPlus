package codebuddy

import "testing"

func TestParseSite(t *testing.T) {
	t.Parallel()

	global, err := ParseSite("")
	if err != nil {
		t.Fatalf("empty region: %v", err)
	}
	if global.Name != SiteNameGlobal || global.APIBaseURL != BaseURLGlobal || global.HeaderDomain != DefaultDomainGlobal {
		t.Fatalf("empty region site = %+v", global)
	}

	cn, err := ParseSite("CN")
	if err != nil {
		t.Fatalf("cn region: %v", err)
	}
	if cn.Name != SiteNameCN || cn.APIBaseURL != BaseURLCN || cn.HeaderDomain != "copilot.tencent.com" {
		t.Fatalf("cn region site = %+v", cn)
	}

	if _, err := ParseSite("us-west"); err == nil {
		t.Fatal("expected error for unknown region")
	}
}

func TestIsGlobalDomain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		domain string
		want   bool
	}{
		{"", false},
		{"www.codebuddy.cn", false},
		{"copilot.tencent.com", false},
		{"www.codebuddy.ai", true},
		{"https://www.codebuddy.ai/login", true},
		{"codebuddy.ai", true},
		{"www.workbuddy.ai", false},
		{"workbuddy.ai", false},
	}
	for _, tc := range cases {
		if got := IsGlobalDomain(tc.domain); got != tc.want {
			t.Errorf("IsGlobalDomain(%q) = %v, want %v", tc.domain, got, tc.want)
		}
	}
}

func TestBillingBaseURLForDomain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		domain string
		want   string
	}{
		{"", BillingBaseCN},
		{"www.codebuddy.cn", BillingBaseCN},
		{"copilot.tencent.com", BillingBaseCN},
		{"www.codebuddy.ai", BillingBaseGlobal},
		{"www.workbuddy.ai", BillingBaseCN},
	}
	for _, tc := range cases {
		if got := BillingBaseURLForDomain(tc.domain); got != tc.want {
			t.Errorf("BillingBaseURLForDomain(%q) = %q, want %q", tc.domain, got, tc.want)
		}
	}
}

func TestAPIBaseURLForDomain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		domain string
		want   string
	}{
		{"", BaseURLCN},
		{"www.codebuddy.cn", BaseURLCN},
		{"copilot.tencent.com", BaseURLCN},
		{"www.codebuddy.ai", BaseURLGlobal},
		{"https://www.codebuddy.ai/", BaseURLGlobal},
		{"www.workbuddy.ai", BaseURLCN},
	}
	for _, tc := range cases {
		if got := APIBaseURLForDomain(tc.domain); got != tc.want {
			t.Errorf("APIBaseURLForDomain(%q) = %q, want %q", tc.domain, got, tc.want)
		}
	}
}
