package search

import (
	"context"
	"time"
)

const maxSearchResponseSize = 2 * 1024 * 1024 // 2MB limit for search API responses

// ProviderName identifies a search provider
type ProviderName string

const (
	ProviderExa    ProviderName = "exa"
	ProviderTavily ProviderName = "tavily"
	ProviderSerper ProviderName = "serper"
	ProviderBrave  ProviderName = "brave"
)

// SearchResult represents a single search result
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// SearchOptions configures search behavior
type SearchOptions struct {
	NumResults     int
	AllowedDomains []string
	BlockedDomains []string
	Timeout        time.Duration
}

// truncateSnippet truncates a snippet to maxLength runes
func truncateSnippet(s string, maxLength int) string {
	runes := []rune(s)
	if len(runes) <= maxLength {
		return s
	}
	return string(runes[:maxLength]) + "..."
}

// getTimeout returns the timeout or default if not set
func getTimeout(opts SearchOptions) time.Duration {
	if opts.Timeout <= 0 {
		return 30 * time.Second
	}
	return opts.Timeout
}

// Provider is the interface for search providers
type Provider interface {
	// DisplayName returns the human-readable name
	DisplayName() string

	// Search performs a web search
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
}

// Meta contains metadata about a search provider
type Meta struct {
	Name           ProviderName
	DisplayName    string
	RequiresAPIKey bool
	EnvVars        []string
}

// AllProviders returns metadata for all search providers
func AllProviders() []Meta {
	return []Meta{
		{
			Name:           ProviderExa,
			DisplayName:    "Exa AI",
			RequiresAPIKey: false,
			EnvVars:        []string{}, // No API key required
		},
		{
			Name:           ProviderTavily,
			DisplayName:    "Tavily",
			RequiresAPIKey: true,
			EnvVars:        []string{"TAVILY_API_KEY"},
		},
		{
			Name:           ProviderSerper,
			DisplayName:    "Serper (Google)",
			RequiresAPIKey: true,
			EnvVars:        []string{"SERPER_API_KEY"},
		},
		{
			Name:           ProviderBrave,
			DisplayName:    "Brave Search",
			RequiresAPIKey: true,
			EnvVars:        []string{"BRAVE_API_KEY"},
		},
	}
}
