package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/workbuddy"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRunWorkBuddyWebDaily_CompletesWebConversation(t *testing.T) {
	var mu sync.Mutex
	prompted := false
	var paths []string
	var statuses []string
	var sawIDE bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("X-IDE-Type") != "" || r.Header.Get("X-IDE-Name") != "" {
			sawIDE = true
		}
		mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/console/as/conversations/":
			if r.Header.Get("Authorization") != "Bearer account-token" || r.Header.Get("X-User-Id") != "user-1" {
				t.Errorf("create headers = %v", r.Header)
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"conversationOrigin":"workbuddy-app"`) || !strings.Contains(string(body), `"model":"deepseek-v4.1-flash"`) {
				t.Errorf("create body = %s", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":"conv-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/console/as/conversations/conv-1/session":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"link":"`+srvURL(r)+`/sandbox","token":"sandbox-token","sessionId":"conv-1","cwd":"/workspace"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/console/as/conversations/conv-1":
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			statuses = append(statuses, string(body))
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":"conv-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/console/as/conversations/conv-1":
			status := "working"
			mu.Lock()
			if prompted {
				status = "completed"
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"status":"`+status+`"}}`)
		case r.URL.Path == "/sandbox" && r.Method == http.MethodGet:
			w.Header().Set("Acp-Connection-Id", "conn-1")
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			_, _ = io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"update\":{\"sessionUpdate\":\"agent_message_chunk\"}}}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
		case r.URL.Path == "/sandbox" && r.Method == http.MethodPost:
			if r.Header.Get("Acp-Connection-Id") != "conn-1" || r.Header.Get("Authorization") != "Bearer sandbox-token" {
				t.Errorf("acp headers = %v", r.Header)
			}
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			paths = append(paths, "ACP "+gjsonMethod(body))
			if strings.Contains(string(body), `"method":"session/prompt"`) {
				prompted = true
				if !strings.Contains(string(body), `"ideType":"WorkBuddy_Web"`) || !strings.Contains(string(body), `"method":"session/prompt"`) {
					t.Errorf("prompt body = %s", body)
				}
			}
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := runWorkBuddyWebDaily(ctx, srv.Client(), srv.URL, "account-token", "user-1", "deepseek-v4.1-flash", 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConversationID != "conv-1" || result.Status != "completed" || result.Chunks != 1 {
		t.Fatalf("result = %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if sawIDE {
		t.Fatal("web conversation carried desktop X-IDE headers")
	}
	joined := strings.Join(paths, "\n")
	if strings.Contains(joined, "/v2/chat/completions") {
		t.Fatalf("desktop chat was called:\n%s", joined)
	}
	for _, want := range []string{
		"POST /console/as/conversations/",
		"GET /console/as/conversations/conv-1/session",
		"GET /sandbox",
		"ACP initialize",
		"ACP session/new",
		"ACP session/set_mode",
		"ACP session/set_model",
		"ACP session/prompt",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "session/load") {
		t.Fatalf("old session/load call still present:\n%s", joined)
	}
	if len(statuses) < 2 || !strings.Contains(statuses[0], `"status":"working"`) || !strings.Contains(statuses[len(statuses)-1], `"status":"completed"`) {
		t.Fatalf("status posts = %#v", statuses)
	}
	if strings.Contains(joined, "GET /console/as/conversations/conv-1\n") || strings.HasSuffix(joined, "GET /console/as/conversations/conv-1") {
		t.Fatalf("still waited on GET status:\n%s", joined)
	}
}

func TestRunWorkBuddyWebDaily_RequiresAgentOutput(t *testing.T) {
	var mu sync.Mutex
	prompted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/console/as/conversations/":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":"conv-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/console/as/conversations/conv-1/session":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"link":"`+srvURL(r)+`/sandbox","token":"sandbox-token","sessionId":"conv-1","cwd":"/workspace"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/console/as/conversations/conv-1":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"status":"completed"`) {
				t.Errorf("posted completed without agent output: %s", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":"conv-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/console/as/conversations/conv-1":
			status := "working"
			mu.Lock()
			if prompted {
				status = "completed"
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":0,"data":{"status":"`+status+`"}}`)
		case r.URL.Path == "/sandbox" && r.Method == http.MethodGet:
			w.Header().Set("Acp-Connection-Id", "conn-1")
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		case r.URL.Path == "/sandbox" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"method":"session/prompt"`) {
				mu.Lock()
				prompted = true
				mu.Unlock()
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := runWorkBuddyWebDaily(ctx, srv.Client(), srv.URL, "account-token", "user-1", "deepseek-v4.1-flash", 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no agent output") {
		t.Fatalf("error = %v", err)
	}
}

func gjsonMethod(body []byte) string {
	const key = `"method":"`
	text := string(body)
	start := strings.Index(text, key)
	if start < 0 {
		return ""
	}
	rest := text[start+len(key):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func TestRunWorkBuddyWebDaily_InternationalOnly(t *testing.T) {
	_, err := RunWorkBuddyWebDaily(context.Background(), &cliproxyauth.Auth{
		Metadata: map[string]any{
			"access_token": "token",
			"user_id":      "user-1",
			"domain":       workbuddy.DefaultDomain,
		},
	}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "international") {
		t.Fatalf("error = %v", err)
	}
}

func srvURL(r *http.Request) string {
	return "http://" + r.Host
}
