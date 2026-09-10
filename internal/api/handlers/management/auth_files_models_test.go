package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCollectAuthFileModelsRefreshTargets(t *testing.T) {
	t.Parallel()

	got := collectAuthFileModelsRefreshTargets(authFileModelsRefreshRequest{
		Name: "ignored.json",
		Files: []authFileModelsRefreshItem{
			{Name: "a.json", AuthIndex: "idx-a"},
			{Name: "a.json", AuthIndex: "idx-a"},
			{Name: " b.json "},
			{Name: ""},
		},
	})
	if len(got) != 2 || got[0].Name != "a.json" || got[1].Name != "b.json" {
		t.Fatalf("files = %#v", got)
	}

	got = collectAuthFileModelsRefreshTargets(authFileModelsRefreshRequest{Names: []string{"x.json", "x.json", "y.json"}})
	if len(got) != 2 || got[0].Name != "x.json" || got[1].Name != "y.json" {
		t.Fatalf("names = %#v", got)
	}

	got = collectAuthFileModelsRefreshTargets(authFileModelsRefreshRequest{Name: "solo.json", AuthIndex: "idx"})
	if len(got) != 1 || got[0].Name != "solo.json" || got[0].AuthIndex != "idx" {
		t.Fatalf("single = %#v", got)
	}
}

func TestRefreshAuthFileModels_RequiresName(t *testing.T) {
	h := NewHandlerWithoutConfigFilePath(&config.Config{}, coreauth.NewManager(nil, nil, nil))
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/models/refresh", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.RefreshAuthFileModels(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRefreshAuthFileModels_RejectsUnsupportedProvider(t *testing.T) {
	authDir := t.TempDir()
	fileName := "codex.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex"}`), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	registerAuthForLookupTest(t, manager, &coreauth.Auth{
		ID:       "codex-1",
		FileName: fileName,
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": filePath,
		},
	})
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := bytes.NewBufferString(`{"name":"codex.json"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/models/refresh", body)
	c.Request.Header.Set("Content-Type", "application/json")
	h.RefreshAuthFileModels(c)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "qoder, codebuddy, and workbuddy") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRefreshAuthFileModels_UsesHook(t *testing.T) {
	authDir := t.TempDir()
	fileName := "qoder.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"qoder"}`), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	registerAuthForLookupTest(t, manager, &coreauth.Auth{
		ID:       "qoder-1",
		FileName: fileName,
		Provider: "qoder",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": filePath,
		},
	})
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.SetAuthModelsRefreshHook(func(_ context.Context, auth *coreauth.Auth) ([]*registry.ModelInfo, error) {
		if auth.ID != "qoder-1" {
			return nil, fmt.Errorf("unexpected auth %s", auth.ID)
		}
		return []*registry.ModelInfo{{ID: "qoder/dfmodel", DisplayName: "DeepSeek", Type: "qoder", OwnedBy: "qoder"}}, nil
	})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := bytes.NewBufferString(`{"name":"qoder.json"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/models/refresh", body)
	c.Request.Header.Set("Content-Type", "application/json")
	h.RefreshAuthFileModels(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ok" || payload.Count != 1 || payload.Models[0].ID != "qoder/dfmodel" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRefreshAuthFileModelsBatch_Partial(t *testing.T) {
	authDir := t.TempDir()
	qoderName := "qoder.json"
	codexName := "codex.json"
	for _, name := range []string{qoderName, codexName} {
		if errWrite := os.WriteFile(filepath.Join(authDir, name), []byte(`{}`), 0o600); errWrite != nil {
			t.Fatal(errWrite)
		}
	}
	manager := coreauth.NewManager(nil, nil, nil)
	registerAuthForLookupTest(t, manager, &coreauth.Auth{
		ID:       "qoder-1",
		FileName: qoderName,
		Provider: "qoder",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": filepath.Join(authDir, qoderName),
		},
	})
	registerAuthForLookupTest(t, manager, &coreauth.Auth{
		ID:       "codex-1",
		FileName: codexName,
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"path": filepath.Join(authDir, codexName),
		},
	})
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.SetAuthModelsRefreshHook(func(_ context.Context, auth *coreauth.Auth) ([]*registry.ModelInfo, error) {
		return []*registry.ModelInfo{{ID: "qoder/dfmodel"}}, nil
	})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := bytes.NewBufferString(`{"names":["qoder.json","codex.json"]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/models/refresh-batch", body)
	c.Request.Header.Set("Content-Type", "application/json")
	h.RefreshAuthFileModelsBatch(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Status    string `json:"status"`
		Refreshed int    `json:"refreshed"`
		Failed    []struct {
			Name string `json:"name"`
		} `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "partial" || payload.Refreshed != 1 || len(payload.Failed) != 1 || payload.Failed[0].Name != "codex.json" {
		t.Fatalf("payload = %#v body=%s", payload, rec.Body.String())
	}
}

func TestAuthFileModelEntriesSkipsEmpty(t *testing.T) {
	t.Parallel()
	got := authFileModelEntries([]*registry.ModelInfo{
		nil,
		{ID: ""},
		{ID: "qoder/dfmodel", DisplayName: "DeepSeek", Type: "qoder", OwnedBy: "qoder"},
	})
	if len(got) != 1 || got[0]["id"] != "qoder/dfmodel" {
		t.Fatalf("entries = %#v", got)
	}
}
