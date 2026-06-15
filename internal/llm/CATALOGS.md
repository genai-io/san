# Provider Catalog Patterns

Each LLM provider lives in `internal/llm/<provider>/` and resolves model
metadata (display names, token limits) through one of three patterns.
Pick the one that matches what the provider's API gives you.

## Pattern A — Flat static catalog

**Use when:** the provider has few, stable models and no per-model pricing.

```go
// catalog.go
var catalog = []llm.ModelInfo{
    {ID: "model-id", Name: "Model", DisplayName: "Model",
     InputTokenLimit: 200000, OutputTokenLimit: 8192},
}

func StaticModels() []llm.ModelInfo { ... }
func CatalogModel(modelID string) (llm.ModelInfo, bool) { ... }
```

`ListModels` returns `StaticModels()` directly — no API call.

**Users:** sensenova

## Pattern B — Catalog with pricing

**Use when:** the provider charges per-token and you track cost.

```go
type modelCatalogEntry struct {
    info    llm.ModelInfo
    pricing pricing
}

var catalog = []modelCatalogEntry{ ... }

func StaticModels() []llm.ModelInfo { ... }
func CatalogModel(modelID string) (llm.ModelInfo, bool) { ... }
func EstimateCost(modelID string, usage llm.Usage) (llm.Money, bool) { ... }
```

`ListModels` returns `StaticModels()` or merges with an API fetch.

**Users:** deepseek, mimo, minmax, ollama

## Pattern C — API-discovered + static fallback functions

**Use when:** the provider has a working `/v1/models` endpoint that lists
model IDs but doesn't include token limits.

```go
// catalog.go — limit lookup functions, NOT a model list
func staticInputLimit(modelID string) int { ... }   // pattern-match on ID
func staticOutputLimit(modelID string) int { ... }
```

`ListModels` calls the API for the model list, then applies the static
functions to fill in limits the API didn't provide.

**Users:** bigmodel, moonshot, volcengine, openai

## Pattern D — Prefix-matching catalog (variant of A)

**Use when:** one logical model has many versioned IDs
(e.g. `claude-opus-4-5-20251101`, `claude-opus-4-5-latest`).

```go
type catalogEntry struct {
    match            func(string) bool   // prefix matcher
    info             llm.ModelInfo
    supportsThinking bool
}
```

**Users:** anthropic

## Which to pick for a new provider

1. Does the provider have a `/v1/models` endpoint? → Pattern C
2. Do you need per-model pricing? → Pattern B
3. Are there many versioned aliases for the same model? → Pattern D
4. Otherwise → Pattern A

## Guardrail

`internal/llm/catalog_check_test.go` verifies that every model in a
static catalog has `InputTokenLimit > 0`. A zero limit means the
context-usage status bar can't render. If a model's limit is genuinely
unknown, don't add it to a static catalog — resolve it dynamically.
