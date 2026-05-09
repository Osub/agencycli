package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRetrieveDisabledDoesNotCallServer(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	result := Retrieve(context.Background(), []string{
		"AGENCYCLI_EVERCORE_URL=" + srv.URL,
	}, Query{Prompt: "hello"})

	if called {
		t.Fatal("server was called while memory integration disabled")
	}
	if len(result.Markdown) != 0 || len(result.Snippets) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestRetrieveEverCoreFormatsEpisodes(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"episodes": []map[string]any{
					{
						"id":      "ep-1",
						"subject": "Build cache decision",
						"summary": "Use a local Go cache for tests.",
						"episode": "The team chose a local cache to reduce repeated downloads.",
						"score":   0.91,
					},
				},
				"query": map[string]any{"text": "cache"},
			},
		})
	}))
	defer srv.Close()

	result := Retrieve(context.Background(), []string{
		"AGENCYCLI_MEMORY_ENABLED=1",
		"AGENCYCLI_EVERCORE_URL=" + srv.URL,
		"AGENCYCLI_EVERCORE_USER_ID=user-1",
		"AGENCYCLI_MEMORY_MAX_SNIPPETS=3",
	}, Query{Project: "p", Agent: "a", TaskTitle: "cache", Prompt: "explain cache"})

	if gotPath != "/api/v1/memories/search" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["method"] != "keyword" {
		t.Fatalf("method = %#v", gotBody["method"])
	}
	filters, ok := gotBody["filters"].(map[string]any)
	if !ok || filters["user_id"] != "user-1" {
		t.Fatalf("filters = %#v", gotBody["filters"])
	}
	md := string(result.Markdown)
	if !strings.Contains(md, "Build cache decision") || !strings.Contains(md, "evercore:episodic_memory") {
		t.Fatalf("markdown missing expected memory: %s", md)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", result.Warnings)
	}
}

func TestRetrieveOpenVikingUsesIdentityHeaders(t *testing.T) {
	var auth, account, user, agent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		account = r.Header.Get("X-OpenViking-Account")
		user = r.Header.Get("X-OpenViking-User")
		agent = r.Header.Get("X-OpenViking-Agent")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"result": map[string]any{
				"memories": []map[string]any{
					{
						"context_type": "memory",
						"uri":          "viking://user/default/memories/preferences/style",
						"abstract":     "Prefer concise Chinese status updates.",
						"score":        0.8,
					},
				},
			},
		})
	}))
	defer srv.Close()

	result := Retrieve(context.Background(), []string{
		"AGENCYCLI_MEMORY_ENABLED=true",
		"AGENCYCLI_OPENVIKING_URL=" + srv.URL,
		"AGENCYCLI_OPENVIKING_API_KEY=ov_test",
		"AGENCYCLI_OPENVIKING_ACCOUNT=acct",
		"AGENCYCLI_OPENVIKING_USER=user",
		"AGENCYCLI_OPENVIKING_AGENT=agent",
	}, Query{Prompt: "status style"})

	if auth != "Bearer ov_test" || account != "acct" || user != "user" || agent != "agent" {
		t.Fatalf("headers auth=%q account=%q user=%q agent=%q", auth, account, user, agent)
	}
	if !strings.Contains(string(result.Markdown), "Prefer concise Chinese status updates.") {
		t.Fatalf("markdown missing OpenViking memory: %s", result.Markdown)
	}
}

func TestInjectLeavesPromptUntouchedWithoutMemory(t *testing.T) {
	if got := Inject("hello\n", Result{}); got != "hello\n" {
		t.Fatalf("Inject without memory = %q", got)
	}
}
