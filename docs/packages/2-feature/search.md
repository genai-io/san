---
package: github.com/genai-io/san/internal/search
layer: feature
---

# search

Pluggable web search backends behind a small consumer-defined `Provider`
interface. Implementations: Exa (default, no API key), Tavily, Serper,
Brave.

## Purpose

The agent's WebFetch / WebSearch tools call into here. Backends are
swappable via the `/search` slash command and `settings.json`'s
`searchProvider` field.

## Contract

This package is the **model citizen** for what consumer-defined
interfaces should look like in this codebase.

```go
package search

// Provider is the interface for search providers.
type Provider interface {
    Name() ProviderName
    DisplayName() string
    RequiresAPIKey() bool
    EnvVars() []string
    IsAvailable() bool
    Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
}

type SearchResult struct {
    Title   string
    URL     string
    Snippet string
}

type SearchOptions struct {
    NumResults     int
    AllowedDomains []string
    BlockedDomains []string
    Timeout        time.Duration
}

type ProviderName string

const (
    ProviderExa    ProviderName = "exa"
    ProviderTavily ProviderName = "tavily"
    ProviderSerper ProviderName = "serper"
    ProviderBrave  ProviderName = "brave"
)

func AllProviders() []Meta   // metadata for the selector UI
```

### Known Violations

Almost none. The `Provider` interface is consumer-shaped (the agent
calls `Search`; metadata methods power the selector and credential
check), the value types are flat structs, and there is no singleton.

- **Minor: `Provider` has 6 methods.** The bulk is metadata
  (`Name`/`DisplayName`/`RequiresAPIKey`/`EnvVars`/`IsAvailable`). A
  cleaner split would lift those into a static `Meta` value (already
  partly done — `AllProviders()` returns `[]Meta`) and leave `Provider`
  as just `Search(...)` plus `Name()`.

## Internals

- `factory.go` — name → constructor map. `New(name)` returns a
  `Provider` for the named backend.
- `exa.go`, `tavily.go`, `brave.go`, `serper.go` — backend
  implementations. Each handles its provider's auth, request shape, and
  response normalization.
- `types.go` — `Provider`, `SearchResult`, `SearchOptions`, and the
  `AllProviders()` metadata table.

## Lifecycle

- No singleton. Callers construct providers per request via `New(name)`
  or hold a reference for the session.
- Stateless: a `Provider` value caches nothing across `Search` calls
  except its API key.

## Tests

```
internal/search/exa_test.go      — Exa request/response.
internal/search/factory_test.go  — name → constructor mapping.
internal/search/tavily_test.go   — Tavily request/response.
```

## See Also

- Code: `internal/search/`
- Tool consumers: [`packages/tool.md`](tool.md) (WebFetch / WebSearch)
- Reference: [`reference/slash-commands.md`](../../reference/slash-commands.md) (`/search`)
- Layer: `feature`
