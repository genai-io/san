package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/genai-io/san/v2/internal/llm"
)

var openAIModelDocsBaseURL = "https://developers.openai.com/api/docs/models/"
var openAIModelDocsHTTPClient = &http.Client{Timeout: 10 * time.Second}

var snapshotDateSuffixRe = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

type modelTokenLimits struct {
	input  int
	output int
}

// FetchModelLimits resolves token limits from OpenAI's public model docs.
// The OpenAI /v1/models API currently returns only basic model metadata
// (id/object/created/owned_by), so context and output limits are pulled from
// the official per-model documentation page on demand.
func (c *Client) FetchModelLimits(ctx context.Context, modelID string) (int, int, error) {
	key := strings.ToLower(strings.TrimSpace(modelID))
	if key == "" {
		return 0, 0, fmt.Errorf("model id is empty")
	}

	c.limitMu.Lock()
	if limits, ok := c.limitCache[key]; ok {
		c.limitMu.Unlock()
		return limits.input, limits.output, nil
	}
	c.limitMu.Unlock()

	var lastErr error
	for _, slug := range openAIModelDocSlugs(modelID) {
		limits, err := fetchOpenAIModelDocLimits(ctx, slug)
		if err == nil {
			c.limitMu.Lock()
			c.limitCache[key] = limits
			c.limitMu.Unlock()
			return limits.input, limits.output, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return 0, 0, lastErr
	}
	return 0, 0, fmt.Errorf("token limits unknown for OpenAI model %q", modelID)
}

func openAIModelDocSlugs(modelID string) []string {
	slug := strings.ToLower(strings.TrimSpace(modelID))
	slug = strings.TrimPrefix(slug, "openai/")
	if slug == "" || strings.Contains(slug, ":") {
		return nil
	}

	slugs := []string{slug}
	if base := snapshotDateSuffixRe.ReplaceAllString(slug, ""); base != slug {
		slugs = append(slugs, base)
	}
	return slugs
}

func fetchOpenAIModelDocLimits(ctx context.Context, slug string) (modelTokenLimits, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIModelDocsBaseURL+slug, nil)
	if err != nil {
		return modelTokenLimits{}, err
	}
	req.Header.Set("User-Agent", "san-token-limit-fetcher")

	resp, err := openAIModelDocsHTTPClient.Do(req)
	if err != nil {
		return modelTokenLimits{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return modelTokenLimits{}, fmt.Errorf("fetch OpenAI model docs for %s: %s", slug, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return modelTokenLimits{}, err
	}
	return parseOpenAIModelDocLimits(body)
}

func parseOpenAIModelDocLimits(body []byte) (modelTokenLimits, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return modelTokenLimits{}, err
	}
	text := normalizeWhitespace(doc.Text())

	input := parseLimitBeforeLabel(text, `context window`)
	output := parseLimitBeforeLabel(text, `max output tokens`)
	if input <= 0 || output <= 0 {
		return modelTokenLimits{}, fmt.Errorf("OpenAI model docs did not include token limits")
	}
	return modelTokenLimits{input: input, output: output}, nil
}

func parseLimitBeforeLabel(text, label string) int {
	re := regexp.MustCompile(`(?i)([\d,]+)\s+` + regexp.QuoteMeta(label))
	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return 0
	}
	n, err := strconv.Atoi(strings.ReplaceAll(match[1], ",", ""))
	if err != nil {
		return 0
	}
	return n
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

var _ llm.ModelLimitsFetcher = (*Client)(nil)
