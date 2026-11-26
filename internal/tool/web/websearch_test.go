package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type searchFixture string

func (f searchFixture) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(f)))}, nil
}

func TestWebSearchResultSummary(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	const first = `Title: Notes\nURL: https://example.org/notes`
	const second = `\n---\nTitle: Agenda\nURL: https://example.org/agenda`
	for count, tc := range []struct{ text, summary string }{
		{"", "No results found for: notes"},
		{first, "Found 1 result for: notes"},
		{first + second, "Found 2 results for: notes"},
	} {
		http.DefaultTransport = searchFixture(`{"result":{"content":[{"type":"text","text":"` + tc.text + `"}]}}`)
		got := (&WebSearchTool{}).Execute(t.Context(), map[string]any{"query": "notes"}, "")
		line, _, _ := strings.Cut(got.Output, "\n")
		if !got.Success || line != tc.summary || got.Metadata.ItemCount != count {
			t.Errorf("%d-result fixture: %q, count=%d", count, got.Output, got.Metadata.ItemCount)
		}
	}
}
