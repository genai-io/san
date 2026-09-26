package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestTavilyLiveSmoke hits the real Tavily API. Gated on TAVILY_LIVE=1 plus a
// real TAVILY_API_KEY so CI never makes outbound calls.
func TestTavilyLiveSmoke(t *testing.T) {
	if os.Getenv("TAVILY_LIVE") != "1" {
		t.Skip("set TAVILY_LIVE=1 to run live Tavily smoke test")
	}
	key := os.Getenv("TAVILY_API_KEY")
	if key == "" {
		t.Skip("TAVILY_API_KEY not set")
	}
	p := NewTavilyProvider(key)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results, err := p.Search(ctx, "golang context cancellation best practices", SearchOptions{NumResults: 3})
	if err != nil {
		t.Fatalf("live search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result from live endpoint")
	}
	for i, r := range results {
		t.Logf("[%d] %s — %s", i+1, r.Title, r.URL)
	}
}

func TestTavilyProviderSearchRequestShape(t *testing.T) {
	var gotMethod, gotAuth, gotContentType string
	var gotReq tavilyRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)

		_ = json.NewEncoder(w).Encode(tavilyResponse{
			Results: []tavilyResult{
				{Title: "Hello", URL: "https://example.com/", Content: "world"},
			},
		})
	}))
	defer server.Close()

	p := &TavilyProvider{apiKey: "tvly-test", endpointOverride: server.URL}
	results, err := p.Search(context.Background(), "q", SearchOptions{NumResults: 4})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if gotAuth != "Bearer tvly-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotReq.Query != "q" || gotReq.MaxResults != 4 || gotReq.SearchDepth != "basic" {
		t.Fatalf("request body = %+v", gotReq)
	}
	if len(results) != 1 || results[0].URL != "https://example.com/" || results[0].Snippet != "world" {
		t.Fatalf("results = %+v", results)
	}
}

func TestTavilyProviderRequiresAPIKey(t *testing.T) {
	p := NewTavilyProvider("")
	_, err := p.Search(context.Background(), "q", SearchOptions{})
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
	if !strings.Contains(err.Error(), tavilyEnvKey) {
		t.Fatalf("error should mention env var name, got %v", err)
	}
}

func TestTavilyProviderAppliesDomainFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(tavilyResponse{
			Results: []tavilyResult{
				{Title: "A", URL: "https://allowed.test/x", Content: "a"},
				{Title: "B", URL: "https://blocked.test/y", Content: "b"},
			},
		})
	}))
	defer server.Close()

	p := &TavilyProvider{apiKey: "k", endpointOverride: server.URL}
	results, err := p.Search(context.Background(), "q", SearchOptions{
		BlockedDomains: []string{"blocked.test"},
	})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after filter, got %d", len(results))
	}
	if results[0].URL != "https://allowed.test/x" {
		t.Fatalf("got %q", results[0].URL)
	}
}

func TestTavilyProviderHTTPErrorPropagates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"bad key"}`))
	}))
	defer server.Close()

	p := &TavilyProvider{apiKey: "bad", endpointOverride: server.URL}
	_, err := p.Search(context.Background(), "q", SearchOptions{})
	if err == nil {
		t.Fatal("expected error on 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected status code in error, got %v", err)
	}
}

func TestAllProvidersIncludesTavily(t *testing.T) {
	var found bool
	for _, m := range AllProviders() {
		if m.Name == ProviderTavily {
			found = true
			if !m.RequiresAPIKey {
				t.Fatal("Tavily metadata should require API key")
			}
			if len(m.EnvVars) != 1 || m.EnvVars[0] != tavilyEnvKey {
				t.Fatalf("Tavily env vars = %v", m.EnvVars)
			}
		}
	}
	if !found {
		t.Fatal("Tavily not in AllProviders()")
	}
}

func TestCreateProviderTavily(t *testing.T) {
	p := CreateProvider(ProviderTavily)
	if _, ok := p.(*TavilyProvider); !ok {
		t.Fatalf("CreateProvider returned %T for Tavily", p)
	}
}
