package web

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/genai-io/san/v2/internal/search"
	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/toolresult"
)

// WebSearchTool searches the web for information.
type WebSearchTool struct{}

func (t *WebSearchTool) Name() string        { return "WebSearch" }
func (t *WebSearchTool) Description() string { return "Search the web for up-to-date information" }
func (t *WebSearchTool) Icon() string        { return toolresult.IconWeb }

func (t *WebSearchTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()

	query, err := tool.RequireString(params, "query")
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	numResults := tool.GetInt(params, "num_results", 10)

	// Get optional domain filters
	var allowedDomains, blockedDomains []string
	if domains, ok := params["allowed_domains"].([]any); ok {
		for _, d := range domains {
			if s, ok := d.(string); ok {
				allowedDomains = append(allowedDomains, s)
			}
		}
	}
	if domains, ok := params["blocked_domains"].([]any); ok {
		for _, d := range domains {
			if s, ok := d.(string); ok {
				blockedDomains = append(blockedDomains, s)
			}
		}
	}

	searchProvider := search.Preferred()

	// Execute search
	opts := search.SearchOptions{
		NumResults:     numResults,
		AllowedDomains: allowedDomains,
		BlockedDomains: blockedDomains,
		Timeout:        30 * time.Second,
	}

	results, err := searchProvider.Search(ctx, query, opts)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("search failed: %v", err))
	}

	// Format results as Markdown
	var sb strings.Builder
	if len(results) == 0 {
		sb.WriteString("No results found for: " + query)
	} else {
		sb.WriteString(fmt.Sprintf("Found %d results for: %s\n\n", len(results), query))
		for _, r := range results {
			sb.WriteString(fmt.Sprintf("- [%s](%s)\n", r.Title, r.URL))
			if r.Snippet != "" {
				sb.WriteString(fmt.Sprintf("  %s\n\n", r.Snippet))
			}
		}
	}

	duration := time.Since(start)

	return toolresult.ToolResult{
		Success: true,
		Output:  sb.String(),
		Metadata: toolresult.ResultMetadata{
			Title:     t.Name(),
			Icon:      t.Icon(),
			Subtitle:  fmt.Sprintf("%s via %s", query, searchProvider.DisplayName()),
			ItemCount: len(results),
			Duration:  duration,
		},
	}
}

func init() {
	tool.Register(&WebSearchTool{})
}
