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
	serperEndpoint = "https://google.serper.dev/search"
	serperEnvKey   = "SERPER_API_KEY"
)

// SerperProvider implements the Serper.dev search provider
type SerperProvider struct {
	apiKey string
}

// NewSerperProvider creates a new Serper provider.
func NewSerperProvider(apiKey string) *SerperProvider {
	return &SerperProvider{apiKey: apiKey}
}

func (p *SerperProvider) DisplayName() string { return "Serper (Google)" }

// serperRequest represents a Serper API request
type serperRequest struct {
	Q   string `json:"q"`
	Num int    `json:"num,omitempty"`
}

// serperResponse represents a Serper API response
type serperResponse struct {
	Organic []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"organic"`
}

// Search performs a web search using Serper
func (p *SerperProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("%s environment variable is not set", serperEnvKey)
	}

	numResults := opts.NumResults
	if numResults <= 0 {
		numResults = 10
	}

	// Build request
	reqBody := serperRequest{
		Q:   query,
		Num: numResults,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	client := &http.Client{Timeout: getTimeout(opts)}
	req, err := http.NewRequestWithContext(ctx, "POST", serperEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", p.apiKey)

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
	var serperResp serperResponse
	if err := json.Unmarshal(respBody, &serperResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to SearchResult and apply domain filtering
	results := make([]SearchResult, 0, len(serperResp.Organic))
	for _, r := range serperResp.Organic {
		// Apply domain filtering (Serper doesn't support it natively)
		if !matchesDomainFilter(r.Link, opts.AllowedDomains, opts.BlockedDomains) {
			continue
		}

		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.Link,
			Snippet: truncateSnippet(r.Snippet, 200),
		})
	}

	return results, nil
}
