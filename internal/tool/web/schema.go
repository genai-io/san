package web

import "github.com/genai-io/san/internal/core"

// Schema returns the model-facing tool definition for WebFetch.
func (t *WebFetchTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "WebFetch",
		Description: `Fetches content from a specified URL and converts HTML to Markdown for readability.

Usage notes:
- The URL must be a fully-formed valid URL
- HTTP URLs will be automatically upgraded to HTTPS
- This tool is read-only and does not modify any files
- Results may be truncated if the content is very large
- For GitHub URLs, prefer using the gh CLI via Bash instead (e.g., gh pr view, gh issue view, gh api)`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch content from",
				},
				"format": map[string]any{
					"type":        "string",
					"description": "Output format: 'markdown' (default) or 'raw'",
				},
			},
			"required": []string{"url"},
		},
	}
}

// Schema returns the model-facing tool definition for WebSearch.
func (t *WebSearchTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "WebSearch",
		Description: `Search the web for up-to-date information. Returns a list of relevant results with titles, URLs, and snippets.
When searching for current information, always use the present year rather than previous years.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query",
				},
				"num_results": map[string]any{
					"type":        "integer",
					"description": "Number of results to return (default: 10)",
				},
				"allowed_domains": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Only include results from these domains",
				},
				"blocked_domains": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Exclude results from these domains",
				},
			},
			"required": []string{"query"},
		},
	}
}
