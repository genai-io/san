package search

import (
	"net/url"
	"strings"

	"github.com/genai-io/san/v2/internal/secret"
	"github.com/genai-io/san/v2/internal/setting"
)

// Preferred returns the preferred search provider from settings, or the default.
func Preferred() Provider {
	if s := setting.DefaultIfInit(); s != nil && s.SearchProvider() != "" {
		return CreateProvider(ProviderName(s.SearchProvider()))
	}
	return GetDefaultProvider()
}

// CreateProvider creates a search provider by name.
// API keys are resolved via os.Getenv first, then the persistent secret store.
func CreateProvider(name ProviderName) Provider {
	switch name {
	case ProviderSerper:
		return NewSerperProvider(secret.Resolve(serperEnvKey))
	case ProviderBrave:
		return NewBraveProvider(secret.Resolve(braveEnvKey))
	case ProviderTavily:
		return NewTavilyProvider(secret.Resolve(tavilyEnvKey))
	case ProviderExa:
		fallthrough
	default:
		return NewExaProvider()
	}
}

// GetDefaultProvider returns the first available search provider
// Priority: Exa (no key needed) > Serper > Brave
func GetDefaultProvider() Provider {
	// Exa is always available (no API key required)
	return NewExaProvider()
}

// matchesDomainFilter checks if a URL matches the domain filter criteria
func matchesDomainFilter(urlStr string, allowedDomains, blockedDomains []string) bool {
	if len(allowedDomains) == 0 && len(blockedDomains) == 0 {
		return true
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return true // If we can't parse, let it through
	}

	host := strings.ToLower(parsedURL.Host)

	// Check blocked domains first
	for _, blocked := range blockedDomains {
		blocked = strings.ToLower(blocked)
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return false
		}
	}

	// Check allowed domains (if any are specified)
	if len(allowedDomains) > 0 {
		for _, allowed := range allowedDomains {
			allowed = strings.ToLower(allowed)
			if host == allowed || strings.HasSuffix(host, "."+allowed) {
				return true
			}
		}
		return false // Didn't match any allowed domain
	}

	return true
}
