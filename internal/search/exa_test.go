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

// TestExaLiveSmoke hits the real Exa MCP endpoint. Gated on EXA_LIVE=1 so
// CI won't make outbound calls.
func TestExaLiveSmoke(t *testing.T) {
	if os.Getenv("EXA_LIVE") != "1" {
		t.Skip("set EXA_LIVE=1 to run live Exa smoke test")
	}
	p := NewExaProvider()
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
		if r.URL == "" {
			t.Errorf("result %d has empty URL", i)
		}
	}
}

func TestParseSSEMessage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single message",
			in:   "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1}\n\n",
			want: `{"jsonrpc":"2.0","id":1}`,
		},
		{
			name: "no data lines falls through",
			in:   `{"jsonrpc":"2.0","id":1}`,
			want: `{"jsonrpc":"2.0","id":1}`,
		},
		{
			name: "literal data inside plain JSON",
			in:   `{"title":"Research data: a survey"}`,
			want: `{"title":"Research data: a survey"}`,
		},
		{
			name: "data line without space after colon",
			in:   "data:{\"a\":1}\n\n",
			want: `{"a":1}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSSEMessage([]byte(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("want %q, got %q", tt.want, string(got))
			}
		})
	}
}

func TestParseExaSearchText(t *testing.T) {
	text := strings.Join([]string{
		"Title: First Result",
		"URL: https://example.com/a",
		"Published: 2024-01-01",
		"Author: Alice",
		"Highlights:",
		"This is the first highlight.",
		"Continued on a second line.",
		"",
		"---",
		"",
		"Title: Second Result",
		"URL: https://docs.example.com/b",
		"Highlights:",
		"Second highlight body.",
	}, "\n")

	results := parseExaSearchText(text)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Title != "First Result" {
		t.Fatalf("title[0] = %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/a" {
		t.Fatalf("url[0] = %q", results[0].URL)
	}
	if !strings.Contains(results[0].Snippet, "first highlight") {
		t.Fatalf("snippet[0] missing highlight: %q", results[0].Snippet)
	}

	if results[1].URL != "https://docs.example.com/b" {
		t.Fatalf("url[1] = %q", results[1].URL)
	}
}

func TestParseExaSearchTextSkipsBlocksWithoutURL(t *testing.T) {
	text := "Title: Only title, no URL\nHighlights:\nbody\n---\nTitle: Real\nURL: https://x.test/\nHighlights:\nreal"
	results := parseExaSearchText(text)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].URL != "https://x.test/" {
		t.Fatalf("url = %q", results[0].URL)
	}
}

func TestParseExaSearchTextTruncatesSnippet(t *testing.T) {
	long := strings.Repeat("a", 300)
	text := "Title: t\nURL: https://x.test/\nHighlights:\n" + long
	results := parseExaSearchText(text)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.HasSuffix(results[0].Snippet, "...") {
		t.Fatalf("expected truncation suffix, got %q", results[0].Snippet)
	}
	if len([]rune(results[0].Snippet)) != 200+3 {
		t.Fatalf("expected 203 runes, got %d", len([]rune(results[0].Snippet)))
	}
}

func TestExaProviderSearchRequestShape(t *testing.T) {
	var gotMethod, gotAccept, gotContentType string
	var gotReq exaMCPRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message\ndata: " + `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"Title: T\nURL: https://example.com/\nHighlights:\nhi"}]}}` + "\n\n"))
	}))
	defer server.Close()

	p := &ExaProvider{endpoint: server.URL}
	results, err := p.Search(context.Background(), "q", SearchOptions{NumResults: 3})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %s", gotMethod)
	}
	if !strings.Contains(gotAccept, "text/event-stream") || !strings.Contains(gotAccept, "application/json") {
		t.Fatalf("Accept header missing required types: %q", gotAccept)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if gotReq.Params.Name != exaToolName {
		t.Fatalf("tool name = %q, want %q", gotReq.Params.Name, exaToolName)
	}
	if gotReq.Params.Arguments.NumResults != 3 {
		t.Fatalf("numResults = %d", gotReq.Params.Arguments.NumResults)
	}
	if len(results) != 1 || results[0].URL != "https://example.com/" {
		t.Fatalf("results = %+v", results)
	}
}

func TestExaProviderSearchAppliesDomainFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := "Title: A\nURL: https://allowed.test/x\nHighlights:\na\n---\nTitle: B\nURL: https://blocked.test/y\nHighlights:\nb"
		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
			},
		})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: "))
		_, _ = w.Write(payload)
		_, _ = w.Write([]byte("\n\n"))
	}))
	defer server.Close()

	p := &ExaProvider{endpoint: server.URL}
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
