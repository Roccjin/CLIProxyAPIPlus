package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	workBuddyResourcePath        = "/v2/billing/meter/get-user-resource"
	workBuddyResourceSummaryPath = "/billing/meter/get-user-resource-summary"
	workBuddyResourcePageSize    = 100
	workBuddyProductCode         = "p_tcaca"
)

// WorkBuddyQuotaPackage is one resource package from the billing meter API.
type WorkBuddyQuotaPackage struct {
	Name       string  `json:"name"`
	Remain     float64 `json:"remain"`
	Used       float64 `json:"used"`
	Size       float64 `json:"size"`
	CycleStart string  `json:"cycle_start,omitempty"`
	CycleEnd   string  `json:"cycle_end,omitempty"`
}

// WorkBuddyQuota is the aggregated credits snapshot for one account.
type WorkBuddyQuota struct {
	Site        string                  `json:"site"`
	TotalRemain float64                 `json:"total_remain"`
	TotalUsed   float64                 `json:"total_used"`
	TotalSize   float64                 `json:"total_size"`
	PackCount   int                     `json:"pack_count"`
	FetchedAt   string                  `json:"fetched_at,omitempty"`
	Packages    []WorkBuddyQuotaPackage `json:"packages"`
}

type workBuddyResourcePackage struct {
	PackageName         string `json:"PackageName"`
	CapacityRemain      int64  `json:"CapacityRemain"`
	CapacityUsed        int64  `json:"CapacityUsed"`
	CapacitySize        int64  `json:"CapacitySize"`
	CycleCapacityRemain int64  `json:"CycleCapacityRemain"`
	CycleCapacityUsed   int64  `json:"CycleCapacityUsed"`
	CycleCapacitySize   int64  `json:"CycleCapacitySize"`
	CycleStartTime      string `json:"CycleStartTime"`
	CycleEndTime        string `json:"CycleEndTime"`
}

type workBuddyResourcePage struct {
	TotalCount  int64                      `json:"TotalCount"`
	TotalDosage int64                      `json:"TotalDosage"`
	Accounts    []workBuddyResourcePackage `json:"Accounts"`
}

// FetchWorkBuddyQuota loads live credits for a WorkBuddy auth from the
// region-correct billing host. International accounts use www.workbuddy.ai;
// China accounts use www.workbuddy.cn.
func FetchWorkBuddyQuota(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) (*WorkBuddyQuota, error) {
	accessToken, userID, domain := workBuddyCredentials(auth)
	if accessToken == "" {
		return nil, fmt.Errorf("workbuddy: missing access token")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	quota, err := fetchWorkBuddyQuotaAtBase(ctx, auth, cfg, workbuddy.BillingBaseURLForDomain(domain), accessToken, userID, domain)
	if err != nil && isWorkBuddyQuotaUnauthorized(err) {
		newToken, newUser, newDomain, refreshErr := refreshWorkBuddyQuotaAuth(ctx, auth, cfg, userID, domain)
		if refreshErr != nil {
			log.Warnf("workbuddy quota: token refresh after unauthorized failed: %v", refreshErr)
			return nil, err
		}
		accessToken, userID, domain = newToken, newUser, newDomain
		quota, err = fetchWorkBuddyQuotaAtBase(ctx, auth, cfg, workbuddy.BillingBaseURLForDomain(domain), accessToken, userID, domain)
	}
	if err != nil {
		log.Warnf("workbuddy quota: fetch failed: %v", err)
	}
	return quota, err
}

func fetchWorkBuddyQuotaAtBase(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, billingBase, accessToken, userID, domain string) (*WorkBuddyQuota, error) {
	quota, err := fetchWorkBuddyQuotaSummary(ctx, auth, cfg, billingBase, accessToken, userID, domain)
	if err == nil {
		return quota, nil
	}
	log.Warnf("workbuddy quota: summary failed, falling back to resource list: %v", err)
	return fetchWorkBuddyQuotaFromBase(ctx, auth, cfg, billingBase, accessToken, userID, domain)
}

func fetchWorkBuddyQuotaFromBase(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, billingBase, accessToken, userID, domain string) (*WorkBuddyQuota, error) {
	now := time.Now().UTC()
	baseBody := map[string]any{
		"PageSize":                 workBuddyResourcePageSize,
		"ProductCode":              workBuddyProductCode,
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": now.Format("2006-01-02 15:04:05"),
		"PackageEndTimeRangeEnd":   now.Add(365 * 101 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
	}

	var all []workBuddyResourcePackage
	var totalCount int64
	var totalDosage int64
	for page := 1; ; page++ {
		body := make(map[string]any, len(baseBody)+1)
		for k, v := range baseBody {
			body[k] = v
		}
		body["PageNumber"] = page

		pageData, err := fetchWorkBuddyResourcePage(ctx, auth, cfg, billingBase, accessToken, userID, domain, body)
		if err != nil {
			return nil, err
		}
		if page == 1 {
			totalCount = pageData.TotalCount
			totalDosage = pageData.TotalDosage
		}
		all = append(all, pageData.Accounts...)
		if len(pageData.Accounts) < workBuddyResourcePageSize || (totalCount > 0 && totalCount <= int64(len(all))) {
			break
		}
	}

	quota := aggregateWorkBuddyPackages(all, totalDosage)
	if workbuddy.IsGlobalDomain(domain) {
		quota.Site = workbuddy.SiteNameGlobal
	} else {
		quota.Site = workbuddy.SiteNameCN
	}
	quota.FetchedAt = now.UTC().Format(time.RFC3339)
	return quota, nil
}

func fetchWorkBuddyResourcePage(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, billingBase, accessToken, userID, domain string, body map[string]any) (workBuddyResourcePage, error) {
	var empty workBuddyResourcePage
	rawBody, err := json.Marshal(body)
	if err != nil {
		return empty, fmt.Errorf("workbuddy: encode resource query: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, billingBase+workBuddyResourcePath, bytes.NewReader(rawBody))
	if err != nil {
		return empty, fmt.Errorf("workbuddy: build resource request: %w", err)
	}
	applyWorkBuddyBillingHeaders(req, accessToken, userID, domain)

	httpClient := newProxyAwareHTTPClient(ctx, cfg, auth, 0)
	var lastErr error
	for attempt := 0; attempt <= workBuddyTransientProviderRetries; attempt++ {
		if attempt > 0 {
			req, err = http.NewRequestWithContext(ctx, http.MethodPost, billingBase+workBuddyResourcePath, bytes.NewReader(rawBody))
			if err != nil {
				return empty, fmt.Errorf("workbuddy: build resource request: %w", err)
			}
			applyWorkBuddyBillingHeaders(req, accessToken, userID, domain)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("workbuddy: resource request failed: %w", err)
			if attempt < workBuddyTransientProviderRetries && isWorkBuddyTransientNetworkError(err) {
				log.Warnf("workbuddy quota: transient network error: %v; retrying", err)
				continue
			}
			return empty, lastErr
		}
		raw, errRead := io.ReadAll(resp.Body)
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("workbuddy: close resource body error: %v", errClose)
		}
		if errRead != nil {
			return empty, fmt.Errorf("workbuddy: read resource response: %w", errRead)
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf("workbuddy: resource status %d", resp.StatusCode)
			if attempt < workBuddyTransientProviderRetries && isWorkBuddyQuotaRetryableStatus(resp.StatusCode) {
				log.Warnf("workbuddy quota: retryable status %d; retrying", resp.StatusCode)
				continue
			}
			return empty, lastErr
		}
		page, err := parseWorkBuddyResourcePage(raw)
		if err != nil {
			return empty, err
		}
		return page, nil
	}
	return empty, lastErr
}

func isWorkBuddyQuotaRetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isWorkBuddyQuotaUnauthorized(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "resource status 401") ||
		strings.Contains(msg, "resource status 403") ||
		strings.Contains(msg, "resource summary status 401") ||
		strings.Contains(msg, "resource summary status 403") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "unauthenticated")
}

func refreshWorkBuddyQuotaAuth(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, userID, domain string) (string, string, string, error) {
	updated, err := NewWorkBuddyExecutor(cfg).Refresh(ctx, auth)
	if err != nil {
		return "", "", "", err
	}
	if updated == nil {
		return "", "", "", fmt.Errorf("workbuddy: empty refresh token response")
	}
	newAccess, newUser, newDomain := workBuddyCredentials(updated)
	if strings.TrimSpace(newAccess) == "" {
		return "", "", "", fmt.Errorf("workbuddy: empty refresh token response")
	}
	adoptWorkBuddyRefreshedAuth(auth, updated)
	if newUser == "" {
		newUser = userID
	}
	if newDomain == "" {
		newDomain = domain
	}
	return newAccess, newUser, newDomain, nil
}

func applyWorkBuddyBillingHeaders(req *http.Request, accessToken, userID, domain string) {
	origin := workbuddy.OriginForDomain(domain)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", workbuddy.UserAgentForAuth(domain))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-Domain", domain)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
}

func parseWorkBuddyResourcePage(raw []byte) (workBuddyResourcePage, error) {
	var empty workBuddyResourcePage
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return empty, fmt.Errorf("workbuddy: decode resource envelope: %w", err)
	}
	if env.Code != 0 {
		return empty, fmt.Errorf("workbuddy: resource code %d: %s", env.Code, env.Msg)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return empty, nil
	}

	var wrapped struct {
		Response struct {
			Data workBuddyResourcePage `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(env.Data, &wrapped); err == nil &&
		(len(wrapped.Response.Data.Accounts) > 0 || wrapped.Response.Data.TotalCount > 0 || wrapped.Response.Data.TotalDosage > 0) {
		return wrapped.Response.Data, nil
	}

	var flat workBuddyResourcePage
	if err := json.Unmarshal(env.Data, &flat); err != nil {
		return empty, fmt.Errorf("workbuddy: decode resource data: %w", err)
	}
	return flat, nil
}

func fetchWorkBuddyQuotaSummary(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, billingBase, accessToken, userID, domain string) (*WorkBuddyQuota, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, billingBase+workBuddyResourceSummaryPath, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("workbuddy: build resource summary request: %w", err)
	}
	applyWorkBuddyBillingHeaders(req, accessToken, userID, domain)
	httpClient := newProxyAwareHTTPClient(ctx, cfg, auth, 0)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("workbuddy: resource summary request failed: %w", err)
	}
	raw, errRead := io.ReadAll(resp.Body)
	if errClose := resp.Body.Close(); errClose != nil {
		log.Errorf("workbuddy: close resource summary body error: %v", errClose)
	}
	if errRead != nil {
		return nil, fmt.Errorf("workbuddy: read resource summary: %w", errRead)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("workbuddy: resource summary status %d", resp.StatusCode)
	}
	quota, err := parseWorkBuddyResourceSummary(raw)
	if err != nil {
		return nil, err
	}
	if workbuddy.IsGlobalDomain(domain) {
		quota.Site = workbuddy.SiteNameGlobal
	} else {
		quota.Site = workbuddy.SiteNameCN
	}
	quota.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	return quota, nil
}

func parseWorkBuddyResourceSummary(raw []byte) (*WorkBuddyQuota, error) {
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("workbuddy: decode resource summary envelope: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("workbuddy: resource summary code %d: %s", env.Code, env.Msg)
	}
	quota := &WorkBuddyQuota{Packages: []WorkBuddyQuotaPackage{}}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return quota, nil
	}
	var data struct {
		Packages []struct {
			PackageCode         string          `json:"PackageCode"`
			PackageName         string          `json:"PackageName"`
			CycleTotalCapacity  json.RawMessage `json:"CycleTotalCapacity"`
			CycleRemainCapacity json.RawMessage `json:"CycleRemainCapacity"`
			CycleUsedCapacity   json.RawMessage `json:"CycleUsedCapacity"`
		} `json:"Packages"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("workbuddy: decode resource summary data: %w", err)
	}
	for _, pkg := range data.Packages {
		remain := roundWorkBuddyQuotaNumber(parseWorkBuddyQuotaNumber(pkg.CycleRemainCapacity))
		used := roundWorkBuddyQuotaNumber(parseWorkBuddyQuotaNumber(pkg.CycleUsedCapacity))
		size := roundWorkBuddyQuotaNumber(parseWorkBuddyQuotaNumber(pkg.CycleTotalCapacity))
		if remain < 0 {
			remain = 0
		}
		if used < 0 {
			used = 0
		}
		if size <= 0 {
			size = remain + used
		}
		name := strings.TrimSpace(pkg.PackageName)
		if name == "" {
			name = strings.TrimSpace(pkg.PackageCode)
		}
		quota.TotalRemain = roundWorkBuddyQuotaNumber(quota.TotalRemain + remain)
		quota.TotalUsed = roundWorkBuddyQuotaNumber(quota.TotalUsed + used)
		quota.TotalSize = roundWorkBuddyQuotaNumber(quota.TotalSize + size)
		quota.Packages = append(quota.Packages, WorkBuddyQuotaPackage{Name: name, Remain: remain, Used: used, Size: size})
	}
	quota.PackCount = len(quota.Packages)
	return quota, nil
}

func parseWorkBuddyQuotaNumber(raw json.RawMessage) float64 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	s := strings.TrimSpace(string(raw))
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func roundWorkBuddyQuotaNumber(v float64) float64 {
	return math.Round(v*100) / 100
}

func aggregateWorkBuddyPackages(all []workBuddyResourcePackage, totalDosage int64) *WorkBuddyQuota {
	quota := &WorkBuddyQuota{Packages: make([]WorkBuddyQuotaPackage, 0, len(all))}
	for _, pkg := range all {
		remain, used, size := workBuddyPackageRemainUsed(pkg)
		quota.TotalRemain += float64(remain)
		quota.TotalUsed += float64(used)
		quota.TotalSize += float64(size)
		quota.Packages = append(quota.Packages, WorkBuddyQuotaPackage{
			Name:       pkg.PackageName,
			Remain:     float64(remain),
			Used:       float64(used),
			Size:       float64(size),
			CycleStart: pkg.CycleStartTime,
			CycleEnd:   pkg.CycleEndTime,
		})
	}
	quota.PackCount = len(quota.Packages)
	if quota.TotalSize > 0 {
		derived := quota.TotalSize - quota.TotalRemain
		if derived < 0 {
			derived = 0
		}
		if derived > quota.TotalUsed {
			quota.TotalUsed = derived
		}
	}
	if float64(totalDosage) > quota.TotalSize {
		quota.TotalSize = float64(totalDosage)
		derived := quota.TotalSize - quota.TotalRemain
		if derived < 0 {
			derived = 0
		}
		if derived > quota.TotalUsed {
			quota.TotalUsed = derived
		}
	}
	return quota
}

func workBuddyPackageRemainUsed(a workBuddyResourcePackage) (remain, used, size int64) {
	if a.CycleCapacitySize > 0 {
		remain = a.CycleCapacityRemain
		size = a.CycleCapacitySize
		if remain < 0 {
			remain = 0
		}
		if remain > size {
			remain = size
		}
		used = size - remain
		if a.CycleCapacityUsed > used {
			used = a.CycleCapacityUsed
			if size >= used {
				remain = size - used
			}
		}
		return remain, used, size
	}
	if a.CycleCapacityRemain > 0 || a.CycleCapacityUsed > 0 {
		remain = a.CycleCapacityRemain
		used = a.CycleCapacityUsed
		if remain < 0 {
			remain = 0
		}
		if used < 0 {
			used = 0
		}
		size = remain + used
		if a.CapacitySize > size {
			size = a.CapacitySize
			if size >= remain {
				used = size - remain
			}
		}
		return remain, used, size
	}
	remain = a.CapacityRemain
	used = a.CapacityUsed
	size = a.CapacitySize
	if remain < 0 {
		remain = 0
	}
	if used < 0 {
		used = 0
	}
	if size <= 0 {
		size = remain + used
	}
	if used == 0 && size > remain {
		used = size - remain
	}
	return remain, used, size
}
