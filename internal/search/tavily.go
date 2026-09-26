package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	tavilyEndpoint = "https://api.tavily.com/search"
	tavilyEnvKey   = "TAVILY_API_KEY"
)

// TavilyProvider implements the Tavily search provider, an LLM-oriented
// search API that returns clean snippets ready for retrieval-augmented use.
type TavilyProvider struct {
	apiKey string
	// endpointOverride overrides the API endpoint. Empty means use tavilyEndpoint.
	// Only set in tests.
	endpointOverride string
}

// NewTavilyProvider creates a new Tavily provider.
func NewTavilyProvider(apiKey string) *TavilyProvider {
	return &TavilyProvider{apiKey: apiKey}
}

func (p *TavilyProvider) DisplayName() string { return "Tavily" }

// tavilyRequest represents a Tavily search API request body.
type tavilyRequest struct {
	Query       string `json:"query"`
	MaxResults  int    `json:"max_results,omitempty"`
	SearchDepth string `json:"search_depth,omitempty"`
}

// tavilyResponse represents a Tavily search API response.
type tavilyResponse struct {
	Results []tavilyResult `json:"results"`
}

type tavilyResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// Search performs a web search using Tavily.
func (p *TavilyProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("%s environment variable is not set", tavilyEnvKey)
	}

	numResults := opts.NumResults
	if numResults <= 0 {
		numResults = 10
	}

	reqBody := tavilyRequest{
		Query:       query,
		MaxResults:  numResults,
		SearchDepth: "basic",
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	client := &http.Client{Timeout: getTimeout(opts)}
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseSize))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var tavilyResp tavilyResponse
	if err := json.Unmarshal(respBody, &tavilyResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	results := make([]SearchResult, 0, len(tavilyResp.Results))
	for _, r := range tavilyResp.Results {
		if !matchesDomainFilter(r.URL, opts.AllowedDomains, opts.BlockedDomains) {
			continue
		}
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: truncateSnippet(r.Content, 200),
		})
	}

	return results, nil
}

// endpoint allows tests to override the API URL.
func (p *TavilyProvider) endpoint() string {
	if p.endpointOverride != "" {
		return p.endpointOverride
	}
	return tavilyEndpoint
}
