package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/genai-io/san/internal/setting"
)

var envInterpolationPattern = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

func (e *Engine) executeHTTPHook(ctx context.Context, hookCmd setting.HookCmd, input HookInput) HookOutcome {
	outcome := HookOutcome{ShouldContinue: true}
	if hookCmd.URL == "" {
		outcome.Error = fmt.Errorf("http hook missing url")
		return outcome
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		outcome.Error = fmt.Errorf("failed to marshal input: %w", err)
		return outcome
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hookCmd.URL, bytes.NewReader(inputJSON))
	if err != nil {
		outcome.Error = err
		return outcome
	}
	req.Header.Set("Content-Type", "application/json")

	allowed := make(map[string]struct{}, len(hookCmd.AllowedEnvVars))
	for _, name := range hookCmd.AllowedEnvVars {
		allowed[name] = struct{}{}
	}
	for key, value := range hookCmd.Headers {
		req.Header.Set(key, interpolateAllowedEnv(value, allowed))
	}

	client := e.getHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		outcome.Error = err
		return outcome
	}
	defer resp.Body.Close()

	const maxHookResponseSize = 10 * 1024 * 1024 // 10MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHookResponseSize))
	if err != nil {
		outcome.Error = err
		return outcome
	}
	if resp.StatusCode >= 400 {
		outcome.Error = fmt.Errorf("http hook failed with status %d", resp.StatusCode)
		return outcome
	}
	return e.parseOutput(strings.TrimSpace(string(body)), outcome)
}

func interpolateAllowedEnv(value string, allowed map[string]struct{}) string {
	return envInterpolationPattern.ReplaceAllStringFunc(value, func(match string) string {
		submatches := envInterpolationPattern.FindStringSubmatch(match)
		if len(submatches) != 2 {
			return ""
		}
		name := submatches[1]
		if _, ok := allowed[name]; !ok {
			return ""
		}
		return os.Getenv(name)
	})
}

func (e *Engine) getHTTPClient() *http.Client {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.httpClient == nil {
		return http.DefaultClient
	}
	return e.httpClient
}
