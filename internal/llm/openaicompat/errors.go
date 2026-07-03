package openaicompat

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3"
)

// IsAuthError reports whether err is an OpenAI-compatible authentication or
// permission failure (HTTP 401/403) — a bad/expired credential or an account
// lacking access, as opposed to a transient/network/shape error.
func IsAuthError(err error) bool {
	var apierr *openai.Error
	if !errors.As(err, &apierr) {
		return false
	}
	return apierr.StatusCode == http.StatusUnauthorized || apierr.StatusCode == http.StatusForbidden
}

// NormalizeAPIError converts OpenAI-compatible auth failures into actionable
// provider-specific guidance while preserving all other errors as-is.
func NormalizeAPIError(providerName string, err error) error {
	if !IsAuthError(err) {
		return err
	}
	var apierr *openai.Error
	errors.As(err, &apierr)

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
