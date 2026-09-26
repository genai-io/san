package search

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	exaMCPEndpoint = "https://mcp.exa.ai/mcp"
	exaToolName    = "web_search_exa"
)

// ExaProvider implements the Exa AI search provider
type ExaProvider struct {
	// endpoint overrides the MCP endpoint URL. Empty means use exaMCPEndpoint.
	// Only set in tests.
	endpoint string
}

// NewExaProvider creates a new Exa provider
func NewExaProvider() *ExaProvider {
	return &ExaProvider{}
}

func (p *ExaProvider) mcpEndpoint() string {
	if p.endpoint != "" {
		return p.endpoint
	}
	return exaMCPEndpoint
}

func (p *ExaProvider) DisplayName() string { return "Exa AI" }

// exaMCPRequest represents a JSON-RPC 2.0 request to Exa MCP
type exaMCPRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Method  string         `json:"method"`
	Params  exaToolRequest `json:"params"`
}

type exaToolRequest struct {
	Name      string             `json:"name"`
	Arguments exaSearchArguments `json:"arguments"`
}

type exaSearchArguments struct {
	Query      string `json:"query"`
	NumResults int    `json:"numResults,omitempty"`
}

// exaMCPResponse represents a JSON-RPC 2.0 response from Exa MCP
type exaMCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *exaMCPError    `json:"error,omitempty"`
}

type exaMCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type exaToolResult struct {
	Content []exaToolContent `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

type exaToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Search performs a web search using Exa MCP endpoint
func (p *ExaProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	numResults := opts.NumResults
	if numResults <= 0 {
		numResults = 8
	}

	mcpReq := exaMCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: exaToolRequest{
			Name: exaToolName,
			Arguments: exaSearchArguments{
				Query:      query,
				NumResults: numResults,
			},
		},
	}

	body, err := json.Marshal(mcpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	client := &http.Client{Timeout: getTimeout(opts)}
	req, err := http.NewRequestWithContext(ctx, "POST", p.mcpEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// Exa MCP endpoint uses Streamable HTTP transport — clients must accept both.
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	jsonBody, err := parseSSEMessage(respBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSE response: %w", err)
	}

	var mcpResp exaMCPResponse
	if err := json.Unmarshal(jsonBody, &mcpResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if mcpResp.Error != nil {
		return nil, fmt.Errorf("exa error: %s", mcpResp.Error.Message)
	}

	var toolResult exaToolResult
	if err := json.Unmarshal(mcpResp.Result, &toolResult); err != nil {
		return nil, fmt.Errorf("failed to parse tool result: %w", err)
	}

	if len(toolResult.Content) == 0 {
		return []SearchResult{}, nil
	}

	if toolResult.IsError {
		return nil, fmt.Errorf("exa tool error: %s", toolResult.Content[0].Text)
	}

	var combined strings.Builder
	for _, c := range toolResult.Content {
		if c.Type == "text" {
			if combined.Len() > 0 {
				combined.WriteString("\n")
			}
			combined.WriteString(c.Text)
		}
	}

	parsed := parseExaSearchText(combined.String())

	results := make([]SearchResult, 0, len(parsed))
	for _, r := range parsed {
		if !matchesDomainFilter(r.URL, opts.AllowedDomains, opts.BlockedDomains) {
			continue
		}
		results = append(results, r)
	}

	return results, nil
}

// parseSSEMessage extracts the JSON payload from a Server-Sent Events response.
// Returns the original body unchanged if no `data:` lines are found, so a
// future server-side switch back to plain JSON would still work.
func parseSSEMessage(body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte("data:")) {
		return body, nil
	}

	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), maxSearchResponseSize)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimPrefix(line, "data:")
		payload = strings.TrimPrefix(payload, " ")
		out.WriteString(payload)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// parseExaSearchText parses Exa's plain-text search output into SearchResults.
//
// The format is N blocks separated by a line containing only "---", each block
// containing "Title:", "URL:", optional "Published:" / "Author:" lines, then a
// "Highlights:" section that runs to the end of the block.
func parseExaSearchText(text string) []SearchResult {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	blocks := splitOnSeparatorLine(text, "---")
	results := make([]SearchResult, 0, len(blocks))

	for _, block := range blocks {
		var title, url string
		var highlights strings.Builder
		inHighlights := false

		scanner := bufio.NewScanner(strings.NewReader(block))
		for scanner.Scan() {
			line := scanner.Text()
			if inHighlights {
				if highlights.Len() > 0 {
					highlights.WriteString(" ")
				}
				highlights.WriteString(strings.TrimSpace(line))
				continue
			}
			switch {
			case strings.HasPrefix(line, "Title:"):
				title = strings.TrimSpace(strings.TrimPrefix(line, "Title:"))
			case strings.HasPrefix(line, "URL:"):
				url = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
			case strings.HasPrefix(line, "Highlights:"):
				inHighlights = true
			}
		}

		if url == "" {
			continue
		}

		results = append(results, SearchResult{
			Title:   title,
			URL:     url,
			Snippet: truncateSnippet(strings.TrimSpace(highlights.String()), 200),
		})
	}

	return results
}

// splitOnSeparatorLine splits text on lines whose trimmed content equals sep.
func splitOnSeparatorLine(text, sep string) []string {
	lines := strings.Split(text, "\n")
	var blocks []string
	var cur strings.Builder
	for _, line := range lines {
		if strings.TrimSpace(line) == sep {
			if cur.Len() > 0 {
				blocks = append(blocks, cur.String())
				cur.Reset()
			}
			continue
		}
		if cur.Len() > 0 {
			cur.WriteString("\n")
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		blocks = append(blocks, cur.String())
	}
	return blocks
}
