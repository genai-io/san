package search

import (
	"testing"
	"time"
)

func TestCreateProviderDefaultsToExa(t *testing.T) {
	if _, ok := CreateProvider("unknown").(*ExaProvider); !ok {
		t.Fatal("expected unknown provider name to fall back to Exa")
	}
}

func TestMatchesDomainFilter(t *testing.T) {
	tests := []struct {
		name           string
		url            string
		allowedDomains []string
		blockedDomains []string
		want           bool
	}{
		{name: "no filters", url: "https://example.com/path", want: true},
		{name: "blocked exact host", url: "https://example.com/path", blockedDomains: []string{"example.com"}, want: false},
		{name: "blocked subdomain", url: "https://docs.example.com/path", blockedDomains: []string{"example.com"}, want: false},
		{name: "allowed exact host", url: "https://example.com/path", allowedDomains: []string{"example.com"}, want: true},
		{name: "allowed subdomain", url: "https://docs.example.com/path", allowedDomains: []string{"example.com"}, want: true},
		{name: "allowed mismatch", url: "https://example.com/path", allowedDomains: []string{"other.com"}, want: false},
		{
			name:           "blocked wins over allowed",
			url:            "https://example.com/path",
			allowedDomains: []string{"example.com"},
			blockedDomains: []string{"example.com"},
			want:           false,
		},
		{name: "invalid url falls through", url: "://bad-url", allowedDomains: []string{"example.com"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesDomainFilter(tt.url, tt.allowedDomains, tt.blockedDomains); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestSearchOptionHelpers(t *testing.T) {
	if got := getTimeout(SearchOptions{}); got != 30*time.Second {
		t.Fatalf("expected zero timeout to fall back to 30s, got %s", got)
	}
	if got := getTimeout(SearchOptions{Timeout: 5 * time.Second}); got != 5*time.Second {
		t.Fatalf("expected custom timeout 5s, got %s", got)
	}

	if got := truncateSnippet("short", 10); got != "short" {
		t.Fatalf("expected short snippet unchanged, got %q", got)
	}
	if got := truncateSnippet("abcdefghij", 5); got != "abcde..." {
		t.Fatalf("expected truncated snippet, got %q", got)
	}
}
