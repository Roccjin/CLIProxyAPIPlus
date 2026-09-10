package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codebuddy"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCodeBuddyPackageRemainUsed(t *testing.T) {
	t.Parallel()

	remain, used, size := codeBuddyPackageRemainUsed(codeBuddyResourcePackage{
		CapacityRemain:      500,
		CapacitySize:        500,
		CycleCapacityRemain: 0,
		CycleCapacitySize:   500,
	})
	if remain != 0 || used != 500 || size != 500 {
		t.Fatalf("cycle exhausted = %d/%d/%d", remain, used, size)
	}

	remain, used, size = codeBuddyPackageRemainUsed(codeBuddyResourcePackage{
		CapacityRemain: 80,
		CapacityUsed:   0,
		CapacitySize:   100,
	})
	if remain != 80 || used != 20 || size != 100 {
		t.Fatalf("lifetime derived = %d/%d/%d", remain, used, size)
	}
}

func TestParseAndAggregateCodeBuddyResource(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"code":0,"msg":"OK","data":{"Response":{"Data":{"TotalCount":2,"TotalDosage":0,"Accounts":[{"PackageName":"体验版","CapacityRemain":0,"CapacitySize":500,"CycleCapacityRemain":0,"CycleCapacitySize":500,"CycleEndTime":"2026-10-01 00:00:00"},{"PackageName":"加油包","CapacityRemain":80,"CapacityUsed":20,"CapacitySize":100}]}}}}`)
	page, err := parseCodeBuddyResourcePage(raw)
	if err != nil {
		t.Fatal(err)
	}
	quota := aggregateCodeBuddyPackages(page.Accounts, page.TotalDosage)
	if quota.PackCount != 2 || quota.TotalRemain != 80 || quota.TotalUsed != 520 || quota.TotalSize != 600 {
		t.Fatalf("aggregate = %+v", quota)
	}
	if quota.Packages[0].Name != "体验版" || quota.Packages[0].CycleEnd == "" {
		t.Fatalf("packages = %+v", quota.Packages)
	}
}

func TestFetchCodeBuddyQuotaFromBase(t *testing.T) {
	t.Parallel()

	var gotDomain string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != codeBuddyResourcePath {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotDomain = r.Header.Get("X-Domain")
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("authorization = %s", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "OK",
			"data": map[string]any{
				"Response": map[string]any{
					"Data": map[string]any{
						"TotalCount":  1,
						"TotalDosage": 100,
						"Accounts": []map[string]any{{
							"PackageName":    "Intl Pack",
							"CapacityRemain": 40,
							"CapacityUsed":   60,
							"CapacitySize":   100,
						}},
					},
				},
			},
		})
	}))
	defer srv.Close()

	auth := &cliproxyauth.Auth{Metadata: map[string]any{
		"access_token": "token",
		"user_id":      "user-1",
		"domain":       codebuddy.DefaultDomainGlobal,
	}}
	quota, err := fetchCodeBuddyQuotaFromBase(context.Background(), auth, nil, srv.URL, "token", "user-1", codebuddy.DefaultDomainGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if gotDomain != codebuddy.DefaultDomainGlobal {
		t.Fatalf("X-Domain = %s", gotDomain)
	}
	if quota.Site != codebuddy.SiteNameGlobal || quota.TotalRemain != 40 || quota.PackCount != 1 {
		t.Fatalf("quota = %+v", quota)
	}
}

func TestFetchCodeBuddyQuotaMissingToken(t *testing.T) {
	t.Parallel()
	_, err := FetchCodeBuddyQuota(context.Background(), &cliproxyauth.Auth{}, nil)
	if err == nil {
		t.Fatal("expected missing token error")
	}
}
