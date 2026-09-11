package workbuddy

import "testing"

func TestParseSite(t *testing.T) {
	t.Parallel()

	global, err := ParseSite("")
	if err != nil {
		t.Fatalf("empty region: %v", err)
	}
	if global.Name != SiteNameGlobal || global.APIBaseURL != BaseURLGlobal || global.Platform != PlatformIntl {
		t.Fatalf("empty region site = %+v", global)
	}

	cn, err := ParseSite("CN")
	if err != nil {
		t.Fatalf("cn region: %v", err)
	}
	if cn.Name != SiteNameCN || cn.APIBaseURL != BaseURLCN || cn.Platform != PlatformCN {
		t.Fatalf("cn region site = %+v", cn)
	}

	if _, err := ParseSite("codebuddy.ai"); err == nil {
		t.Fatal("codebuddy.ai must not parse as a WorkBuddy region")
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
		{"www.workbuddy.cn", false},
		{"www.workbuddy.ai", true},
		{"https://www.workbuddy.ai/login", true},
		{"workbuddy.ai", true},
		{"www.codebuddy.ai", false},
		{"www.codebuddy.cn", false},
	}
	for _, tc := range cases {
		if got := IsGlobalDomain(tc.domain); got != tc.want {
			t.Errorf("IsGlobalDomain(%q) = %v, want %v", tc.domain, got, tc.want)
		}
	}
}

func TestAPIBaseURLForDomain(t *testing.T) {
	t.Parallel()

	if got := APIBaseURLForDomain("www.workbuddy.ai"); got != BaseURLGlobal {
		t.Errorf("global = %s", got)
	}
	if got := APIBaseURLForDomain("www.workbuddy.cn"); got != BaseURLCN {
		t.Errorf("cn = %s", got)
	}
}

func TestIsWorkBuddyDomain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		domain string
		want   bool
	}{
		{"", false},
		{"www.workbuddy.ai", true},
		{"workbuddy.ai", true},
		{"https://www.workbuddy.cn/login", true},
		{"www.codebuddy.ai", false},
		{"copilot.tencent.com", false},
	}
	for _, tc := range cases {
		if got := IsWorkBuddyDomain(tc.domain); got != tc.want {
			t.Errorf("IsWorkBuddyDomain(%q) = %v, want %v", tc.domain, got, tc.want)
		}
	}
}

func TestMigrateLegacyCodeBuddyMetadata(t *testing.T) {
	t.Parallel()

	legacy := map[string]any{"type": "codebuddy", "domain": "www.workbuddy.ai", "access_token": "tok"}
	if !MigrateLegacyCodeBuddyMetadata(legacy) {
		t.Fatal("expected legacy WorkBuddy file to migrate")
	}
	if got, _ := legacy["type"].(string); got != "workbuddy" {
		t.Fatalf("type = %q, want workbuddy", got)
	}

	codebuddyCN := map[string]any{"type": "codebuddy", "domain": "www.codebuddy.cn"}
	if MigrateLegacyCodeBuddyMetadata(codebuddyCN) {
		t.Fatal("codebuddy.cn must not migrate")
	}
}
