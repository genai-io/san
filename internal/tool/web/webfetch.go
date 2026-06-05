package web

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/toolresult"
)

const (
	maxResponseSize = 5 * 1024 * 1024 // 5MB
	httpTimeout     = 30 * time.Second
)

// WebFetchTool fetches content from URLs
type WebFetchTool struct{}

func (t *WebFetchTool) Name() string        { return "WebFetch" }
func (t *WebFetchTool) Description() string { return "Fetch content from a URL" }
func (t *WebFetchTool) Icon() string        { return toolresult.IconWeb }

func (t *WebFetchTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()

	urlStr, err := tool.RequireString(params, "url")
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		urlStr = "https://" + urlStr
	}

	format := "markdown"
	if f := tool.GetString(params, "format"); f != "" {
		format = f
	}

	// Create HTTP client with SSRF protection, timeout, and redirect validation
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() {
					return nil, fmt.Errorf("blocked: request to private/internal address %s", ip.IP)
				}
			}
			dialAddr := addr
			if port != "" {
				dialAddr = net.JoinHostPort(host, port)
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, dialAddr)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   httpTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to non-HTTP scheme %q blocked", req.URL.Scheme)
			}
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), "invalid URL: "+err.Error())
	}

	// Set user agent
	req.Header.Set("User-Agent", "San/1.0")

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), "request failed: "+err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	// Check status code
	if resp.StatusCode >= 400 {
		return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
	}

	// Read body with size limit
	limitedReader := io.LimitReader(resp.Body, maxResponseSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), "failed to read response: "+err.Error())
	}
	sizeTruncated := len(body) > maxResponseSize
	if sizeTruncated {
		body = body[:maxResponseSize]
	}

	// Convert content based on format
	content := string(body)
	contentType := resp.Header.Get("Content-Type")

	if format == "markdown" && strings.Contains(contentType, "text/html") {
		// Convert HTML to Markdown
		converter := md.NewConverter("", true, nil)
		markdown, err := converter.ConvertString(content)
		if err == nil {
			content = markdown
		}
	}

	// Truncate if too long
	const maxLines = 2000
	truncated := sizeTruncated
	lines := strings.Split(content, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		content = strings.Join(lines, "\n")
		truncated = true
	}

	duration := time.Since(start)

	result := toolresult.ToolResult{
		Success: true,
		Output:  content,
		Metadata: toolresult.ResultMetadata{
			Title:      t.Name(),
			Icon:       t.Icon(),
			Subtitle:   urlStr,
			Size:       int64(len(body)),
			StatusCode: resp.StatusCode,
			LineCount:  len(lines),
			Duration:   duration,
			Truncated:  truncated,
		},
	}

	return result
}

func init() {
	tool.Register(&WebFetchTool{})
}
