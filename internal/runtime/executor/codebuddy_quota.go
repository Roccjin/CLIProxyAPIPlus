package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	codeBuddyResourcePath     = "/v2/billing/meter/get-user-resource"
	codeBuddyResourcePageSize = 100
	codeBuddyProductCode      = "p_tcaca"
)

// CodeBuddyQuotaPackage is one resource package from the billing meter API.
type CodeBuddyQuotaPackage struct {
	Name       string `json:"name"`
	Remain     int64  `json:"remain"`
	Used       int64  `json:"used"`
	Size       int64  `json:"size"`
	CycleStart string `json:"cycle_start,omitempty"`
	CycleEnd   string `json:"cycle_end,omitempty"`
}

// CodeBuddyQuota is the aggregated credits snapshot for one account.
type CodeBuddyQuota struct {
	Site        string                  `json:"site"`
	TotalRemain int64                   `json:"total_remain"`
	TotalUsed   int64                   `json:"total_used"`
	TotalSize   int64                   `json:"total_size"`
	PackCount   int                     `json:"pack_count"`
	FetchedAt   string                  `json:"fetched_at,omitempty"`
	Packages    []CodeBuddyQuotaPackage `json:"packages"`
}

type codeBuddyResourcePackage struct {
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

type codeBuddyResourcePage struct {
	TotalCount  int64                      `json:"TotalCount"`
	TotalDosage int64                      `json:"TotalDosage"`
	Accounts    []codeBuddyResourcePackage `json:"Accounts"`
}

// FetchCodeBuddyQuota loads live credits for a CodeBuddy auth from the
// region-correct billing host. International accounts use www.codebuddy.ai;
// China accounts use www.codebuddy.cn.
func FetchCodeBuddyQuota(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) (*CodeBuddyQuota, error) {
	accessToken, userID, domain := codeBuddyCredentials(auth)
	if accessToken == "" {
		return nil, fmt.Errorf("codebuddy: missing access token")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return fetchCodeBuddyQuotaFromBase(ctx, auth, cfg, codebuddy.BillingBaseURLForDomain(domain), accessToken, userID, domain)
}

func fetchCodeBuddyQuotaFromBase(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, billingBase, accessToken, userID, domain string) (*CodeBuddyQuota, error) {
	now := time.Now()
	baseBody := map[string]any{
		"PageSize":                 codeBuddyResourcePageSize,
		"ProductCode":              codeBuddyProductCode,
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": now.Format("2006-01-02 15:04:05"),
		"PackageEndTimeRangeEnd":   now.Add(365 * 101 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
	}

	var all []codeBuddyResourcePackage
	var totalCount int64
	var totalDosage int64
	for page := 1; ; page++ {
		body := make(map[string]any, len(baseBody)+1)
		for k, v := range baseBody {
			body[k] = v
		}
		body["PageNumber"] = page

		pageData, err := fetchCodeBuddyResourcePage(ctx, auth, cfg, billingBase, accessToken, userID, domain, body)
		if err != nil {
			return nil, err
		}
		if page == 1 {
			totalCount = pageData.TotalCount
			totalDosage = pageData.TotalDosage
		}
		all = append(all, pageData.Accounts...)
		if len(pageData.Accounts) < codeBuddyResourcePageSize || (totalCount > 0 && totalCount <= int64(len(all))) {
			break
		}
	}

	quota := aggregateCodeBuddyPackages(all, totalDosage)
	if codebuddy.IsGlobalDomain(domain) {
		quota.Site = codebuddy.SiteNameGlobal
	} else {
		quota.Site = codebuddy.SiteNameCN
	}
	quota.FetchedAt = now.UTC().Format(time.RFC3339)
	return quota, nil
}

func fetchCodeBuddyResourcePage(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config, billingBase, accessToken, userID, domain string, body map[string]any) (codeBuddyResourcePage, error) {
	var empty codeBuddyResourcePage
	rawBody, err := json.Marshal(body)
	if err != nil {
		return empty, fmt.Errorf("codebuddy: encode resource query: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, billingBase+codeBuddyResourcePath, bytes.NewReader(rawBody))
	if err != nil {
		return empty, fmt.Errorf("codebuddy: build resource request: %w", err)
	}
	applyCodeBuddyBillingHeaders(req, accessToken, userID, domain)

	httpClient := newProxyAwareHTTPClient(ctx, cfg, auth, 0)
	resp, err := httpClient.Do(req)
	if err != nil {
		return empty, fmt.Errorf("codebuddy: resource request failed: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("codebuddy: close resource body error: %v", errClose)
		}
	}()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return empty, fmt.Errorf("codebuddy: read resource response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return empty, fmt.Errorf("codebuddy: resource status %d", resp.StatusCode)
	}

	page, err := parseCodeBuddyResourcePage(raw)
	if err != nil {
		return empty, err
	}
	return page, nil
}

func applyCodeBuddyBillingHeaders(req *http.Request, accessToken, userID, domain string) {
	origin := codebuddy.OriginForDomain(domain)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codebuddy.UserAgent)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-Domain", domain)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
}

func parseCodeBuddyResourcePage(raw []byte) (codeBuddyResourcePage, error) {
	var empty codeBuddyResourcePage
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return empty, fmt.Errorf("codebuddy: decode resource envelope: %w", err)
	}
	if env.Code != 0 {
		return empty, fmt.Errorf("codebuddy: resource code %d: %s", env.Code, env.Msg)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return empty, fmt.Errorf("codebuddy: empty resource data")
	}

	var wrapped struct {
		Response struct {
			Data codeBuddyResourcePage `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(env.Data, &wrapped); err == nil &&
		(len(wrapped.Response.Data.Accounts) > 0 || wrapped.Response.Data.TotalCount > 0 || wrapped.Response.Data.TotalDosage > 0) {
		return wrapped.Response.Data, nil
	}

	var flat codeBuddyResourcePage
	if err := json.Unmarshal(env.Data, &flat); err != nil {
		return empty, fmt.Errorf("codebuddy: decode resource data: %w", err)
	}
	return flat, nil
}

func aggregateCodeBuddyPackages(all []codeBuddyResourcePackage, totalDosage int64) *CodeBuddyQuota {
	quota := &CodeBuddyQuota{Packages: make([]CodeBuddyQuotaPackage, 0, len(all))}
	for _, pkg := range all {
		remain, used, size := codeBuddyPackageRemainUsed(pkg)
		quota.TotalRemain += remain
		quota.TotalUsed += used
		quota.TotalSize += size
		quota.Packages = append(quota.Packages, CodeBuddyQuotaPackage{
			Name:       pkg.PackageName,
			Remain:     remain,
			Used:       used,
			Size:       size,
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
	if totalDosage > quota.TotalSize {
		quota.TotalSize = totalDosage
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

func codeBuddyPackageRemainUsed(a codeBuddyResourcePackage) (remain, used, size int64) {
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
