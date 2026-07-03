package openaicompat

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3"
)

// asAuthError extracts the underlying OpenAI error when err is an
// authentication or permission failure (HTTP 401/403); it reports false for any
// other error.
func asAuthError(err error) (*openai.Error, bool) {
	var apierr *openai.Error
	if !errors.As(err, &apierr) {
		return nil, false
	}
	if apierr.StatusCode != http.StatusUnauthorized && apierr.StatusCode != http.StatusForbidden {
		return nil, false
	}
	return apierr, true
}

// IsAuthError reports whether err is an OpenAI-compatible authentication or
// permission failure (HTTP 401/403) — a bad/expired credential or an account
// lacking access, as opposed to a transient/network/shape error.
func IsAuthError(err error) bool {
	_, ok := asAuthError(err)
	return ok
}

// NormalizeAPIError converts OpenAI-compatible auth failures into actionable
// provider-specific guidance while preserving all other errors as-is.
func NormalizeAPIError(providerName string, err error) error {
	apierr, ok := asAuthError(err)
	if !ok {
		return err
	}

	providerLabel, envVar := providerAuthHelp(providerName)
	msg := strings.TrimSpace(apierr.Message)
	if msg == "" {
		msg = strings.TrimSpace(apierr.RawJSON())
	}

	if envVar == "" {
		if msg == "" {
			return fmt.Errorf("%s authentication failed; reconnect the provider with /model", providerLabel)
		}
		return fmt.Errorf("%s authentication failed: %s. Reconnect the provider with /model", providerLabel, msg)
	}

	if msg == "" {
		return fmt.Errorf("%s authentication failed; check %s and reconnect the provider with /model", providerLabel, envVar)
	}
	return fmt.Errorf("%s authentication failed: %s. Check %s and reconnect the provider with /model", providerLabel, msg, envVar)
}

func providerAuthHelp(providerName string) (label string, envVar string) {
	base := providerName
	if idx := strings.IndexByte(base, ':'); idx >= 0 {
		base = base[:idx]
	}

	switch strings.ToLower(base) {
	case "moonshot":
		return "Moonshot", "MOONSHOT_API_KEY"
	case "openai":
		return "OpenAI", "OPENAI_API_KEY"
	case "alibaba":
		return "Alibaba", "DASHSCOPE_API_KEY"
	case "minmax":
		return "MiniMax", "MINIMAX_API_KEY"
	case "deepseek":
		return "DeepSeek", "DEEPSEEK_API_KEY"
	default:
		if base == "" {
			return "Provider", ""
		}
		return strings.ToUpper(base[:1]) + base[1:], ""
	}
}
