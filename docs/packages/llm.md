---
package: github.com/genai-io/san/internal/llm
layer: feature
---

# llm

Provider registry, model store, and client factory for every LLM backend
(Anthropic, OpenAI, Google, Moonshot, Alibaba, MiniMax, Z.ai/GLM, DeepSeek,
Ollama, plus the generic openai-compat shim). Provider implementations live in
`internal/llm/<name>/` subpackages.

## Purpose

The agent loop talks to LLMs through `core.LLM` (see
[`packages/core.md`](core.md)). This package owns the *machinery around*
that contract — discovering providers, persisting the user's chosen
provider/model, switching between them at runtime, and tracking cost and
streaming details for each call.

## Contract

Active LLM provider/model handle and `*Client` factory. Wraps the package-level *Setup (Store + Provider + CurrentModel) under a mutex. The package exposes `*ClientFactory` directly — no Service interface.

```go
package llm

// ClientFactory is the opaque handle. Type exported; fields unexported.
type ClientFactory struct { /* internal fields */ }

func (s *ClientFactory) Provider() Provider
func (s *ClientFactory) SetProvider(p Provider)
func (s *ClientFactory) ModelID() string
func (s *ClientFactory) CurrentModel() *CurrentModelInfo
func (s *ClientFactory) SetCurrentModel(info *CurrentModelInfo)
func (s *ClientFactory) NewClient(model string, maxTokens int) *Client
func (s *ClientFactory) Store() *Store
func (s *ClientFactory) ListProviders() map[Name][]Info

// Package-level access
func Initialize(opts Options)
func Default() *ClientFactory
func SetDefaultClientFactory(s *ClientFactory)  // test-only
func ResetDefaultClientFactory()          // test-only
```


## Internals

- `service` (`service.go`) — singleton implementation wrapping a `Setup`
  struct (mutex + current Provider/Model + Store).
- `Provider` registry (`registry.go`) — discovery, dynamic model list
  fetching (per memory: prefer `/models` over hardcoded catalogs).
- `Client` (consolidated `Infer` path) — adapts a `Provider` + model into
  `core.LLM`, tracks per-call token counts, streams `core.Chunk`, applies
  retry/cost logic via `logging.go` and `money.go`.
- `Store` (`store.go`) — persists user's provider connections under
  `~/.san/providers.json`; tracks current model.
- `stream/` — provider-side helpers for SSE parsing.
- Provider subpackages: `anthropic/`, `openai/`, `google/`, `moonshot/`,
  `alibaba/`, `bigmodel/`, `minmax/`, `deepseek/`, `ollama/`, `openaicompat/`.

## Lifecycle

- Construction: `Initialize(Options{})` loads `~/.san/providers.json`,
  picks the last-used provider (or the first connectable one), and stores
  it.
- Switching: `/model` slash command calls `SetCurrentModel` + reload.
- Per-call: `NewClient(model, maxTokens)` produces a `*Client` for one
  inference; the client wraps `Provider.Infer`.

## Tests

```
internal/llm/llm_test.go        — Client.Infer plumbing.
internal/llm/store_test.go      — provider config persistence.
internal/llm/fake_llm.go        — test double consumed by other packages.
```

## See Also

- Code: `internal/llm/`
- Primitive: [`packages/core.md`](core.md) (`LLM` interface)
- Cost tracking surfaced via [`packages/session.md`](session.md) recorder.
- Layer: `feature`
