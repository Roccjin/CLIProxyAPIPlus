package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestWorkBuddyPackageRemainUsed(t *testing.T) {
	t.Parallel()

	remain, used, size := workBuddyPackageRemainUsed(workBuddyResourcePackage{
		CapacityRemain:      500,
		CapacitySize:        500,
		CycleCapacityRemain: 0,
		CycleCapacitySize:   500,
	})
	if remain != 0 || used != 500 || size != 500 {
		t.Fatalf("cycle exhausted = %d/%d/%d", remain, used, size)
	}

	remain, used, size = workBuddyPackageRemainUsed(workBuddyResourcePackage{
		CapacityRemain: 80,
		CapacityUsed:   0,
		CapacitySize:   100,
	})
	if remain != 80 || used != 20 || size != 100 {
		t.Fatalf("lifetime derived = %d/%d/%d", remain, used, size)
	}
}

func TestParseAndAggregateWorkBuddyResource(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"code":0,"msg":"OK","data":{"Response":{"Data":{"TotalCount":2,"TotalDosage":0,"Accounts":[{"PackageName":"体验版","CapacityRemain":0,"CapacitySize":500,"CycleCapacityRemain":0,"CycleCapacitySize":500,"CycleEndTime":"2026-10-01 00:00:00"},{"PackageName":"加油包","CapacityRemain":80,"CapacityUsed":20,"CapacitySize":100}]}}}}`)
	page, err := parseWorkBuddyResourcePage(raw)
	if err != nil {
		t.Fatal(err)
	}
	quota := aggregateWorkBuddyPackages(page.Accounts, page.TotalDosage)
	if quota.PackCount != 2 || quota.TotalRemain != 80 || quota.TotalUsed != 520 || quota.TotalSize != 600 {
		t.Fatalf("aggregate = %+v", quota)
	}
	if quota.Packages[0].Name != "体验版" || quota.Packages[0].CycleEnd == "" {
		t.Fatalf("packages = %+v", quota.Packages)
	}
}

func TestFetchWorkBuddyQuotaFromBase(t *testing.T) {
	t.Parallel()

	var gotDomain string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != workBuddyResourcePath {
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
		"domain":       workbuddy.DefaultDomainGlobal,
	}}
	quota, err := fetchWorkBuddyQuotaFromBase(context.Background(), auth, nil, srv.URL, "token", "user-1", workbuddy.DefaultDomainGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if gotDomain != workbuddy.DefaultDomainGlobal {
		t.Fatalf("X-Domain = %s", gotDomain)
	}
	if quota.Site != workbuddy.SiteNameGlobal || quota.TotalRemain != 40 || quota.PackCount != 1 {
		t.Fatalf("quota = %+v", quota)
	}
}

func TestFetchWorkBuddyQuotaMissingToken(t *testing.T) {
	t.Parallel()
	_, err := FetchWorkBuddyQuota(context.Background(), &cliproxyauth.Auth{}, nil)
	if err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestParseWorkBuddyResourcePage_EmptyDataIsZeroQuota(t *testing.T) {
	t.Parallel()

	page, err := parseWorkBuddyResourcePage([]byte(`{"code":0,"msg":"OK","data":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Accounts) != 0 || page.TotalCount != 0 {
		t.Fatalf("page = %+v", page)
	}
}

func TestFetchWorkBuddyQuotaFromBase_RetriesServerError(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"code":1,"msg":"busy"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "OK",
			"data": map[string]any{
				"Response": map[string]any{
					"Data": map[string]any{
						"TotalCount":  1,
						"TotalDosage": 10,
						"Accounts": []map[string]any{{
							"PackageName":    "Retry Pack",
							"CapacityRemain": 7,
							"CapacityUsed":   3,
							"CapacitySize":   10,
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
		"domain":       workbuddy.DefaultDomainGlobal,
	}}
	quota, err := fetchWorkBuddyQuotaFromBase(context.Background(), auth, nil, srv.URL, "token", "user-1", workbuddy.DefaultDomainGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if quota.TotalRemain != 7 || quota.PackCount != 1 {
		t.Fatalf("quota = %+v", quota)
	}
}

func TestIsWorkBuddyQuotaUnauthorized(t *testing.T) {
	t.Parallel()
	if !isWorkBuddyQuotaUnauthorized(fmt.Errorf("workbuddy: resource status 401")) {
		t.Fatal("expected 401 to be unauthorized")
	}
	if isWorkBuddyQuotaUnauthorized(fmt.Errorf("workbuddy: resource status 500")) {
		t.Fatal("500 is not unauthorized")
	}
}
