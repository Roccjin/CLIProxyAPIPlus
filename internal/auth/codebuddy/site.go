package codebuddy

import (
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
)

const (
	// BaseURLCN is the China chat/auth gateway.
	// Global JWTs are rejected here (APISIX 401); CN tokens belong on this host.
	BaseURLCN = "https://copilot.tencent.com"
	// BaseURLGlobal is the international chat/auth gateway (www.codebuddy.ai).
	BaseURLGlobal = "https://www.codebuddy.ai"

	// BaseURL is the historical CN API base. New code should use APIBaseURLForDomain.
	BaseURL = BaseURLCN

	// BillingBaseCN hosts CN resource-package / credits APIs.
	BillingBaseCN = "https://www.codebuddy.cn"
	// BillingBaseGlobal hosts international credits APIs.
	BillingBaseGlobal = BaseURLGlobal

	DefaultDomain       = "www.codebuddy.cn"
	DefaultDomainGlobal = "www.codebuddy.ai"

	ClientVersion = "2.156.0"
	UserAgent     = "CLI/" + ClientVersion + " CodeBuddy/" + ClientVersion

	// CLIExtName is the official CodeBuddy CLI package name used in /v2/report.
	CLIExtName = "@tencent-ai/codebuddy-code"
	// CLICommit and CLIReleaseDateMS are the 2.156.0 CLI build fingerprint
	// captured from the official client /v2/report payload.
	CLICommit        = "fee4a881785f81589716563417322d748c3901c6"
	CLIReleaseDateMS = int64(1789923107760)

	SiteNameCN     = "cn"
	SiteNameGlobal = "global"
)

// Site describes a CodeBuddy login/API realm.
type Site struct {
	Name         string
	APIBaseURL   string
	HeaderDomain string
	TokenDomain  string
}

// SiteCN returns the China CodeBuddy realm (copilot.tencent.com / www.codebuddy.cn).
func SiteCN() Site {
	return Site{
		Name:         SiteNameCN,
		APIBaseURL:   BaseURLCN,
		HeaderDomain: "copilot.tencent.com",
		TokenDomain:  DefaultDomain,
	}
}

// SiteGlobal returns the international CodeBuddy realm (www.codebuddy.ai).
func SiteGlobal() Site {
	return Site{
		Name:         SiteNameGlobal,
		APIBaseURL:   BaseURLGlobal,
		HeaderDomain: DefaultDomainGlobal,
		TokenDomain:  DefaultDomainGlobal,
	}
}

// ParseSite maps a login region string to a CodeBuddy site.
// Empty values default to the international site.
func ParseSite(value string) (Site, error) {
	switch normalizeHost(value) {
	case "", "global", "intl", "international", "ai", "codebuddy.ai", "www.codebuddy.ai":
		return SiteGlobal(), nil
	case "cn", "china", "codebuddy.cn", "www.codebuddy.cn":
		return SiteCN(), nil
	default:
		return Site{}, fmt.Errorf("codebuddy: unknown region %q (use global or cn)", value)
	}
}

// IsGlobalDomain reports whether domain belongs to the international service.
func IsGlobalDomain(domain string) bool {
	d := normalizeHost(domain)
	if d == "" {
		return false
	}
	return d == "codebuddy.ai" || strings.HasSuffix(d, ".codebuddy.ai")
}

// APIBaseURLForDomain returns the chat/auth API host for a stored token domain.
// CN tokens use copilot.tencent.com; international tokens must stay on their own host.
// Legacy WorkBuddy files stored as type "codebuddy" keep their original hosts
// until they are migrated to the WorkBuddy provider.
func APIBaseURLForDomain(domain string) string {
	if workbuddy.IsWorkBuddyDomain(domain) {
		return workbuddy.APIBaseURLForDomain(domain)
	}
	d := normalizeHost(domain)
	switch {
	case d == "codebuddy.ai" || strings.HasSuffix(d, ".codebuddy.ai"):
		return BaseURLGlobal
	default:
		return BaseURLCN
	}
}

// BillingBaseURLForDomain returns the credits/resource-package host.
// CN billing lives on www.codebuddy.cn, not the copilot.tencent.com chat gateway.
func BillingBaseURLForDomain(domain string) string {
	if workbuddy.IsWorkBuddyDomain(domain) {
		return workbuddy.BillingBaseURLForDomain(domain)
	}
	d := normalizeHost(domain)
	switch {
	case d == "codebuddy.ai" || strings.HasSuffix(d, ".codebuddy.ai"):
		return BillingBaseGlobal
	default:
		return BillingBaseCN
	}
}

// OriginForDomain returns the Origin/Referer base for billing and login-adjacent calls.
func OriginForDomain(domain string) string {
	if workbuddy.IsWorkBuddyDomain(domain) {
		return workbuddy.OriginForDomain(domain)
	}
	d := normalizeHost(domain)
	switch {
	case d == "codebuddy.ai" || strings.HasSuffix(d, ".codebuddy.ai"):
		return BaseURLGlobal
	default:
		return BillingBaseCN
	}
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
