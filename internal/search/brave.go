package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	braveEndpoint = "https://api.search.brave.com/res/v1/web/search"
	braveEnvKey   = "BRAVE_API_KEY"
)

// BraveProvider implements the Brave Search provider
type BraveProvider struct {
	apiKey string
}

// NewBraveProvider creates a new Brave provider.
func NewBraveProvider(apiKey string) *BraveProvider {
	return &BraveProvider{apiKey: apiKey}
}

func (p *BraveProvider) DisplayName() string { return "Brave Search" }

// braveResponse represents a Brave Search API response
type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// Search performs a web search using Brave Search
func (p *BraveProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("%s environment variable is not set", braveEnvKey)
	}

	numResults := opts.NumResults
	if numResults <= 0 {
		numResults = 10
	}

	// Build URL with query parameters
	u, err := url.Parse(braveEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	q := u.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", numResults))
	u.RawQuery = q.Encode()

	// Create HTTP request
	client := &http.Client{Timeout: getTimeout(opts)}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", p.apiKey)

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseSize))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Read response with size limit
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response
	var braveResp braveResponse
	if err := json.Unmarshal(respBody, &braveResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to SearchResult and apply domain filtering
	results := make([]SearchResult, 0, len(braveResp.Web.Results))
	for _, r := range braveResp.Web.Results {
		// Apply domain filtering (Brave supports it but we do client-side for consistency)
		if !matchesDomainFilter(r.URL, opts.AllowedDomains, opts.BlockedDomains) {
			continue
		}

		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: truncateSnippet(r.Description, 200),
		})
	}

	return results, nil
}
