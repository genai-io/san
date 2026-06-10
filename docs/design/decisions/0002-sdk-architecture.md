# ADR-0002: Public SDK Architecture — San Agent SDK & LLM Client SDK

## Status

Accepted — 2026-06-05. Amended 2026-06-08 (moved to `genai-io/sdk-go`).

## Context

San is currently a single-binary terminal application. All Go packages live under
`internal/`, which means no external Go module can import and reuse San's
capabilities. This creates friction for teams who want to:

1. **Embed a San Agent** in their own Go applications — e.g., CI/CD pipelines,
   chatbots, custom developer tools — without forking the repo or shelling out
   to the `san` binary.
2. **Call LLMs directly** through San's multi-provider abstraction — leveraging
   San's battle-tested provider integrations (Anthropic, OpenAI, Google, DeepSeek,
   Moonshot, MiniMax, Alibaba, BigModel, Ollama) without pulling in each
   provider's SDK separately.

The internal architecture already has clean, stable contracts at the `internal/core`
layer (`Agent`, `LLM`, `Tool`, `Tools`, `System`, `Message`, `Event`). The LLM
provider layer (`internal/llm`) also has a well-defined `Provider` interface with
nine backends. These contracts are mature enough to expose as a supported public API.

The initial proposal placed SDK packages inside `genai-io/san/pkg/`. After
[discussion](https://github.com/genai-io/san/pull/120#issuecomment-4644774244),
the decision was revised to host them in the existing `genai-io/sdk-go` repo:

1. **Separation of concerns**: `san` is the agent itself; the SDK is a
   consumer-facing library. Independent versioning, dependency management,
   and release cycles.
2. **The repo already exists**: `genai-io/sdk-go` was created with the
   description "Go SDK providing LLM client tools" — it is the natural home
   for this work.
3. **Broader audience**: SDK consumers should not need to pull the entire
   `san` codebase just to use the LLM client or agent SDK.

## Decision

### 1. Create two public SDK packages in `genai-io/sdk-go`

```
sdk-go/
├── pkg/
│   ├── llm/           # LLM Client SDK — direct model access
│   │   ├── client.go      # Client: wraps Provider, implements core.LLM
│   │   ├── provider.go    # Provider interface + registry
│   │   ├── options.go     # CompletionOptions, streaming config
│   │   └── models.go      # ModelInfo, ListModels
│   └── san/           # San Agent SDK — full agent lifecycle
│       ├── agent.go       # Agent: construction, config, run, think-act
│       ├── events.go      # Event stream types for external consumers
│       └── options.go     # AgentOptions (functional options pattern)
└── docs/
    └── design/
        └── decisions/
            └── 0002-sdk-architecture.md  # this document
```

**Rule:** SDK packages are thin public facades. They import from `genai-io/san`
(`internal/llm`, `internal/agent`, `internal/core`) and re-export the stable
subset. No business logic lives in `pkg/` — only type adaptation, ergonomic
constructors, and documentation.

### 2. LLM Client SDK (`pkg/llm`)

**Purpose:** Let external Go programs call LLMs through San's provider abstraction
without depending on the full agent runtime.

**Public API surface:**

```go
// pkg/llm — import "github.com/genai-io/sdk-go/pkg/llm"

// ---- Provider ----

// Provider is the interface all LLM backends implement.
type Provider interface {
    Stream(ctx context.Context, opts CompletionOptions) <-chan StreamChunk
    ListModels(ctx context.Context) ([]ModelInfo, error)
    Name() string
}

// NewProvider creates a Provider by name + auth method.
// Supported names: anthropic, openai, google, deepseek, moonshot,
//                  alibaba, bigmodel, minmax, ollama.
// Supported auth methods: api_key, vertex, bedrock.
func NewProvider(ctx context.Context, name string, authMethod string) (Provider, error)

// ListProviders returns metadata for all registered providers.
func ListProviders() []ProviderMeta

// ---- Client (convenience wrapper) ----

// Client wraps a Provider with model selection, token tracking,
// and a synchronous Complete() helper.
type Client struct { ... }

func NewClient(p Provider, model string) *Client

// Infer implements core.LLM — streaming inference.
func (c *Client) Infer(ctx context.Context, req InferRequest) (<-chan Chunk, error)

// Complete runs inference and collects all chunks into a single response.
// Convenience method for non-streaming use cases.
func (c *Client) Complete(ctx context.Context, req InferRequest) (*InferResponse, error)

// ---- Types (re-exported from san's internal packages) ----

type CompletionOptions struct { Model, SystemPrompt string; Messages []Message; ... }
type InferRequest struct { System string; Messages []Message; Tools []ToolSchema }
type InferResponse struct { Content, Thinking string; ToolCalls []ToolCall; ... }
type Chunk struct { Text, Thinking string; Done bool; Response *InferResponse; Err error }
type StreamChunk struct { Type ChunkType; Text string; ... }
type ModelInfo struct { ID, Name, DisplayName string; InputTokenLimit, OutputTokenLimit int }
type Message struct { ... }
type ToolSchema struct { ... }
type ToolCall struct { ... }
```

**Usage example:**

```go
import "github.com/genai-io/sdk-go/pkg/llm"

func main() {
    provider, _ := llm.NewProvider(ctx, "anthropic", "api_key")
    client := llm.NewClient(provider, "claude-sonnet-4-6")

    resp, err := client.Complete(ctx, llm.InferRequest{
        System: "You are a helpful assistant.",
        Messages: []llm.Message{
            {Role: "user", Content: "Hello!"},
        },
    })
    fmt.Println(resp.Content)
}
```

### 3. San Agent SDK (`pkg/san`)

**Purpose:** Let external Go programs create and run a full San agent — the same
agent loop that powers the `san` CLI, but programmatically embeddable.

**Public API surface:**

```go
// pkg/san — import "github.com/genai-io/sdk-go/pkg/san"

// ---- Agent ----

// Agent is a San agent instance.
type Agent struct { ... }

// New creates an agent with the given options.
// Required: WithLLM, WithSystem. Tools default to empty (no tools).
func New(opts ...AgentOption) (*Agent, error)

// Run starts the agent's main loop. Blocks until ctx is cancelled or a
// stop signal is received. Emits events to the channel returned by Events().
//
//   ctx := context.Background()
//   agent, _ := san.New(san.WithLLM(llmClient), san.WithSystem(sys))
//   events := agent.Events()
//   go func() { for e := range events { handle(e) } }()
//   agent.Inbox() <- san.NewUserMessage("Write a function")
//   agent.Run(ctx)
func (a *Agent) Run(ctx context.Context) error

// ThinkAct runs a single inference-action cycle synchronously.
// For callers who want explicit turn-by-turn control instead of the run loop.
func (a *Agent) ThinkAct(ctx context.Context) (*Result, error)

// Inbox returns the send channel for user messages and signals.
func (a *Agent) Inbox() chan<- Message

// Events returns the receive channel for agent lifecycle events.
// Must be consumed before Run() is called to avoid deadlock.
func (a *Agent) Events() <-chan Event

// Close shuts down the agent gracefully.
func (a *Agent) Close() error

// ---- AgentOption (functional options) ----

type AgentOption func(*agentConfig)

func WithLLM(llm core.LLM) AgentOption                // required
func WithSystem(system core.System) AgentOption        // required
func WithTools(tools core.Tools) AgentOption           // optional, nil = no tools
func WithID(id string) AgentOption                     // optional
func WithMaxSteps(n int) AgentOption                   // optional
func WithCWD(dir string) AgentOption                   // optional
func WithCompactFunc(fn CompactFunc) AgentOption       // optional

// ---- Event ----

// Event is a lifecycle event emitted by the agent during Run().
type Event struct {
    Type   EventType  // OnStart, OnStop, OnChunk, OnTurn, PreTool, PostTool, ...
    Source string
    Data   any
}

// Convenience accessors:
func (e Event) Chunk() (Chunk, bool)
func (e Event) Result() (Result, bool)
func (e Event) ToolCall() (ToolCall, bool)
func (e Event) Error() (error, bool)
```

**Usage example:**

```go
import (
    "github.com/genai-io/sdk-go/pkg/san"
    "github.com/genai-io/sdk-go/pkg/llm"
)

func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // 1. Create LLM client
    provider, _ := llm.NewProvider(ctx, "anthropic", "api_key")
    llmClient := llm.NewClient(provider, "claude-sonnet-4-6")

    // 2. Create system prompt
    sys := core.NewSystem(core.SystemSections{
        {Name: "base", Content: "You are a coding assistant."},
    })

    // 3. Create agent
    agent, _ := san.New(
        san.WithLLM(llmClient),
        san.WithSystem(sys),
    )

    // 4. Consume events
    go func() {
        for evt := range agent.Events() {
            if c, ok := evt.Chunk(); ok && c.Text != "" {
                fmt.Print(c.Text)
            }
        }
    }()

    // 5. Send a message and run
    agent.Inbox() <- san.NewUserMessage("Write a Go HTTP server.")
    agent.Run(ctx)
}
```

### 4. Dependency direction

`sdk-go` depends on `san` for the internal implementation packages. External
consumers import only `sdk-go`:

```
External consumer
     │
     ├── github.com/genai-io/sdk-go/pkg/san   (Agent SDK)
     │       │
     │       └── github.com/genai-io/sdk-go/pkg/llm   (LLM Client SDK)
     │               │
     │               └── github.com/genai-io/san/internal/llm   (provider implementations)
     │                       │
     │                       └── github.com/genai-io/san/internal/core  (stable contracts)
     │
     └── github.com/genai-io/sdk-go/pkg/llm   (LLM SDK — also usable standalone)
```

- `sdk-go/pkg/llm` depends on `san/internal/llm` and `san/internal/core`.
- `sdk-go/pkg/san` depends on `sdk-go/pkg/llm`, `san/internal/agent`, `san/internal/core`.
- Both SDKs can be imported independently — use `pkg/llm` without `pkg/san` if
  you only need model access.
- External consumers only need `go get github.com/genai-io/sdk-go`; the `san`
  dependency is transitive.

**Future extraction option:** If the transitive `san` dependency proves too
heavy, shared contracts (`core.LLM`, `core.Agent`, `core.Message`, etc.) can
be extracted into `sdk-go` as canonical definitions, with `san` then depending
on `sdk-go`. This inverts the current direction but requires more upfront work
and is deferred until needed.

### 5. What stays in `internal/` (san repo)

All implementation details remain in `genai-io/san`:

- `internal/llm/{anthropic,openai,google,...}` — per-provider adapters
- `internal/agent` — agent loop implementation
- `internal/tool` — built-in tool implementations
- `internal/session` — transcript persistence
- `internal/app` — TUI shell (not relevant to SDK users)
- `internal/hook`, `internal/mcp`, `internal/plugin`, `internal/skill`, etc.

The SDK packages are **facades**, not rewrites. They import from `san/internal/`
and re-export the stable subset.

### 6. Go module

The SDKs live in the existing `genai-io/sdk-go` module:

```
github.com/genai-io/sdk-go           # root module (go.mod at repo root)
├── pkg/llm/                         # import "github.com/genai-io/sdk-go/pkg/llm"
└── pkg/san/                         # import "github.com/genai-io/sdk-go/pkg/san"
```

`sdk-go/go.mod` declares a dependency on `github.com/genai-io/san`:

```
module github.com/genai-io/sdk-go

go 1.24

require github.com/genai-io/san v0.X.Y
```

External repos add a single `go get github.com/genai-io/sdk-go` to get both SDKs.

## Consequences

### Positive

- **Ecosystem unlock.** Any Go project can embed San's agent loop or call LLMs
  through San's provider abstraction. CI/CD systems, chatbots, custom tools,
  and internal platforms all become consumers without shelling out to the CLI.
- **Incremental adoption.** Teams can start with `pkg/llm` (just call models),
  then graduate to `pkg/san` (full agent with tool loop) as their needs grow.
- **Thin facade, low maintenance.** SDK packages are ~200–400 lines each —
  mostly type aliases, constructor functions, and doc comments. The real logic
  stays in `san/internal/` where it can evolve without breaking SDK consumers
  (as long as the public types remain stable).
- **Dogfooding.** The `cmd/san` CLI itself can migrate to use `sdk-go/pkg/san`,
  proving the SDK is real and keeping it from rotting.
- **Independent versioning.** `sdk-go` and `san` have separate release cycles.
  A breaking change in `san/internal/` does not force a major bump of `sdk-go`
  unless the public API surface changes. Conversely, SDK improvements
  (ergonomics, docs, examples) can ship without a `san` release.
- **Smaller consumer surface.** Consumers import `sdk-go` — a lightweight module
  with clear public packages — rather than the full `san` module with its
  sprawling `internal/` tree. The intent is clear: "I'm using the SDK."

### Negative / costs

- **Public API commitment.** Once `pkg/` packages are imported by external repos,
  breaking changes require major version bumps or deprecation cycles. The current
  `san/internal/` types (e.g., `core.Message`, `core.ToolSchema`) become part of
  the public contract. We must be deliberate about which types we re-export.
- **Cross-repo coordination.** Changes that span `sdk-go` and `san` (e.g.,
  adding a new provider, changing `core.LLM` interface) require coordinated PRs
  across two repos. CI must verify compatibility.
- **Transitive dependency.** Consumers of `sdk-go` still pull in `san` as a
  transitive dependency. This is acceptable for now; extraction of shared
  contracts into `sdk-go` (inverting the dependency) can be done later if
  the `san` dependency proves too heavy.
- **Godoc/surface area.** The exported API must be documented to the same
  standard as the internal packages. Public docs are a commitment.
- **Versioning pressure.** `sdk-go` must adopt semver from day one. `v0.x.y`
  allows breaking changes; `v1.0.0` locks the public API.

### Migration path for `cmd/san`

Once `sdk-go/pkg/san` is stable, `cmd/san/main.go` can be refactored to use it:

```
Before:
  cmd/san → internal/app → internal/agent → internal/llm → internal/core

After:
  cmd/san → internal/app → sdk-go/pkg/san → sdk-go/pkg/llm → internal/llm → internal/core
```

This is a non-breaking internal refactor that proves the SDK works for the
primary use case (CLI agent) before external consumers depend on it. The `san`
repo gains a dependency on `sdk-go`, which is appropriate since `cmd/san`
consumes the same SDK that external users will consume.

## Implementation Plan

### Phase 0: Transfer ADR & issues (~0.5 days)

1. Transfer this ADR doc to `genai-io/sdk-go` under `docs/design/decisions/`.
2. Transfer related issues (e.g., #123 on provider client abstraction) to `sdk-go`.
3. Set up `sdk-go/go.mod` with a dependency on `genai-io/san`.
4. Update this ADR in `san` to note the transfer and point to `sdk-go`.

### Phase 1: LLM Client SDK in `sdk-go/pkg/llm` — ~2 days

1. Create `pkg/llm/` directory in `sdk-go`.
2. Define public types: `Provider`, `Client`, `CompletionOptions`,
   `InferRequest`, `InferResponse`, `Chunk`, `StreamChunk`, `ModelInfo`,
   `Message`, `ToolSchema`, `ToolCall`.
3. Implement `NewProvider()` — thin wrapper around `san/internal/llm` registry.
4. Implement `Client` — thin wrapper around `san/internal/llm.Client`.
5. Write godoc examples.
6. Add tests that exercise all nine providers with mock backends.

### Phase 2: San Agent SDK in `sdk-go/pkg/san` — ~3 days

1. Create `pkg/san/` directory in `sdk-go`.
2. Define public types: `Agent`, `Event`, `Result`, `AgentOption`.
3. Implement `New()` — thin wrapper around `san/internal/agent` construction.
4. Implement `Run()`, `ThinkAct()`, `Inbox()`, `Events()`.
5. Write godoc examples (standalone agent, agent with tools).
6. Add integration tests with a mock LLM.

### Phase 3: Dogfood & Polish — ~2 days

1. Refactor `cmd/san` to use `sdk-go/pkg/san` for agent construction.
2. Add `docs/packages/pkg-llm.md` and `docs/packages/pkg-san.md` in `sdk-go`.
3. Write a migration guide for early adopters.

## References

- [PR #120 discussion](https://github.com/genai-io/san/pull/120) — decision to move to `sdk-go`.
- [genai-io/sdk-go](https://github.com/genai-io/sdk-go) — target repo for SDK packages.
- [`docs/architecture.md`](../../architecture.md) — system-level architecture overview.
- [`internal/core/agent.go`](../../../internal/core/agent.go) — `Agent` interface contract.
- [`internal/core/llm.go`](../../../internal/core/llm.go) — `LLM` interface contract.
- [`internal/llm/types.go`](../../../internal/llm/types.go) — `Provider` interface and types.
- [`internal/llm/llm.go`](../../../internal/llm/llm.go) — `Client` implementation.
- [`internal/agent/build.go`](../../../internal/agent/build.go) — agent construction.
- [ADR-0001: Layered package architecture](./0001-layered-package-architecture.md) — layer model this design extends.
