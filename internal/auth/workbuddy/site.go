package workbuddy

import (
	"fmt"
	"strings"
)

const (
	// BaseURLCN is the China WorkBuddy chat/auth gateway.
	BaseURLCN = "https://www.workbuddy.cn"
	// BaseURLGlobal is the international WorkBuddy chat/auth gateway.
	BaseURLGlobal = "https://www.workbuddy.ai"

	// BaseURL is the historical alias of the CN API base.
	BaseURL = BaseURLCN

	BillingBaseCN     = BaseURLCN
	BillingBaseGlobal = BaseURLGlobal

	DefaultDomain       = "www.workbuddy.cn"
	DefaultDomainGlobal = "www.workbuddy.ai"

	AppVersion = "5.5.2"
	CLIVersion = "2.137.1"

	UserAgentChat     = "WorkBuddy/" + AppVersion + " WorkBuddy/" + AppVersion + " CLI/" + CLIVersion
	UserAgentAuthCN   = UserAgentChat
	UserAgentAuthIntl = "workbuddy-ai/" + AppVersion + " workbuddy-ai/" + AppVersion + " CLI/" + CLIVersion
	UserAgent         = UserAgentChat
	ClientVersion     = AppVersion
	IDEType           = "WorkBuddy"

	PlatformCN     = "workbuddy"
	PlatformIntl   = "workbuddy-ai"
	SiteNameCN     = "cn"
	SiteNameGlobal = "global"
)

// Site describes a WorkBuddy login/API realm.
type Site struct {
	Name         string
	APIBaseURL   string
	HeaderDomain string
	TokenDomain  string
	Platform     string
	UserAgent    string
}

// SiteCN returns the China WorkBuddy realm (www.workbuddy.cn).
func SiteCN() Site {
	return Site{
		Name:         SiteNameCN,
		APIBaseURL:   BaseURLCN,
		HeaderDomain: DefaultDomain,
		TokenDomain:  DefaultDomain,
		Platform:     PlatformCN,
		UserAgent:    UserAgentAuthCN,
	}
}

// SiteGlobal returns the international WorkBuddy realm (www.workbuddy.ai).
func SiteGlobal() Site {
	return Site{
		Name:         SiteNameGlobal,
		APIBaseURL:   BaseURLGlobal,
		HeaderDomain: DefaultDomainGlobal,
		TokenDomain:  DefaultDomainGlobal,
		Platform:     PlatformIntl,
		UserAgent:    UserAgentAuthIntl,
	}
}

// ParseSite maps a login region string to a WorkBuddy site.
// Empty values default to the international site.
func ParseSite(value string) (Site, error) {
	switch normalizeHost(value) {
	case "", "global", "intl", "international", "ai", "workbuddy.ai", "www.workbuddy.ai":
		return SiteGlobal(), nil
	case "cn", "china", "workbuddy.cn", "www.workbuddy.cn":
		return SiteCN(), nil
	default:
		return Site{}, fmt.Errorf("workbuddy: unknown region %q (use global or cn)", value)
	}
}

// IsGlobalDomain reports whether domain belongs to the international WorkBuddy service.
func IsGlobalDomain(domain string) bool {
	d := normalizeHost(domain)
	if d == "" {
		return false
	}
	return d == "workbuddy.ai" || strings.HasSuffix(d, ".workbuddy.ai")
}

// APIBaseURLForDomain returns the chat/auth API host for a stored token domain.
func APIBaseURLForDomain(domain string) string {
	if IsGlobalDomain(domain) {
		return BaseURLGlobal
	}
	return BaseURLCN
}

// BillingBaseURLForDomain returns the credits host for a stored token domain.
func BillingBaseURLForDomain(domain string) string {
	if IsGlobalDomain(domain) {
		return BillingBaseGlobal
	}
	return BillingBaseCN
}

// OriginForDomain returns the Origin/Referer base for billing and login-adjacent calls.
func OriginForDomain(domain string) string {
	if IsGlobalDomain(domain) {
		return BaseURLGlobal
	}
	return BaseURLCN
}

// UserAgentForAuth returns the OAuth/refresh User-Agent for a token domain.
func UserAgentForAuth(domain string) string {
	if IsGlobalDomain(domain) {
		return UserAgentAuthIntl
	}
	return UserAgentAuthCN
}

func normalizeHost(value string) string {
	d := strings.ToLower(strings.TrimSpace(value))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	if i := strings.IndexByte(d, '/'); i >= 0 {
		d = d[:i]
	}
	return strings.TrimSuffix(d, ".")
}
