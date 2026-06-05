# ADR-0002: Public SDK Architecture — San Agent SDK & LLM Client SDK

## Status

Proposed — 2026-06-05.

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

## Decision

### 1. Create two public SDK packages under `pkg/`

```
pkg/
├── llm/           # LLM Client SDK — direct model access
│   ├── client.go      # Client: wraps Provider, implements core.LLM
│   ├── provider.go    # Provider interface + registry (re-export from internal/llm)
│   ├── options.go     # CompletionOptions, streaming config
│   └── models.go      # ModelInfo, ListModels
└── san/           # San Agent SDK — full agent lifecycle
    ├── agent.go       # Agent: construction, config, run, think-act
    ├── events.go      # Event stream types for external consumers
    └── options.go     # AgentOptions (functional options pattern)
```

**Rule:** SDK packages are thin public facades. They export types and functions
that delegate to `internal/` packages. No business logic lives in `pkg/` — only
type adaptation, ergonomic constructors, and documentation.

### 2. LLM Client SDK (`pkg/llm`)

**Purpose:** Let external Go programs call LLMs through San's provider abstraction
without depending on the full agent runtime.

**Public API surface:**

```go
// pkg/llm — import "github.com/genai-io/san/pkg/llm"

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

// ---- Types (re-exported from core / llm) ----

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
import "github.com/genai-io/san/pkg/llm"

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
// pkg/san — import "github.com/genai-io/san/pkg/san"

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
    "github.com/genai-io/san/pkg/san"
    "github.com/genai-io/san/pkg/llm"
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

```
External repo
     │
     ├── github.com/genai-io/san/pkg/san   (Agent SDK)
     │       │
     │       └── github.com/genai-io/san/pkg/llm   (LLM Client SDK)
     │               │
     │               └── internal/llm   (provider implementations)
     │                       │
     │                       └── internal/core  (stable contracts)
     │
     └── github.com/genai-io/san/pkg/llm   (LLM SDK — also usable standalone)
```

- `pkg/llm` depends on `internal/llm` and `internal/core`.
- `pkg/san` depends on `pkg/llm`, `internal/agent`, `internal/core`.
- Both SDKs can be imported independently — use `pkg/llm` without `pkg/san` if
  you only need model access.

### 5. What stays in `internal/`

All implementation details remain internal:

- `internal/llm/{anthropic,openai,google,...}` — per-provider adapters
- `internal/agent` — agent loop implementation
- `internal/tool` — built-in tool implementations
- `internal/session` — transcript persistence
- `internal/app` — TUI shell (not relevant to SDK users)
- `internal/hook`, `internal/mcp`, `internal/plugin`, `internal/skill`, etc.

The SDK packages are **facades**, not rewrites. They import from `internal/` and
re-export the stable subset.

### 6. Go module

No new Go module. The SDKs are subdirectories of the existing
`github.com/genai-io/san` module:

```
github.com/genai-io/san           # root module (go.mod at repo root)
├── pkg/llm/                      # import "github.com/genai-io/san/pkg/llm"
└── pkg/san/                      # import "github.com/genai-io/san/pkg/san"
```

External repos add a single `go get github.com/genai-io/san` to get both SDKs.

## Consequences

### Positive

- **Ecosystem unlock.** Any Go project can embed San's agent loop or call LLMs
  through San's provider abstraction. CI/CD systems, chatbots, custom tools,
  and internal platforms all become consumers without shelling out to the CLI.
- **Incremental adoption.** Teams can start with `pkg/llm` (just call models),
  then graduate to `pkg/san` (full agent with tool loop) as their needs grow.
- **Thin facade, low maintenance.** SDK packages are ~200–400 lines each —
  mostly type aliases, constructor functions, and doc comments. The real logic
  stays in `internal/` where it can evolve without breaking SDK consumers
  (as long as the public types remain stable).
- **Dogfooding.** The `cmd/san` CLI itself can migrate to use `pkg/san`,
  proving the SDK is real and keeping it from rotting.
- **No new module boundary.** Single `go.mod` means no multi-module versioning
  complexity. SDK consumers get the same version as the CLI.

### Negative / costs

- **Public API commitment.** Once `pkg/` packages are imported by external repos,
  breaking changes require major version bumps or deprecation cycles. The current
  `internal/` types (e.g., `core.Message`, `core.ToolSchema`) become part of the
  public contract. We must be deliberate about which types we re-export.
- **Internal ↔ pkg coupling.** SDK packages import `internal/`, which means
  we cannot move SDKs to a separate module without first extracting the shared
  contracts. This is acceptable for now — the single-module approach is simpler
  and extraction can happen later if needed.
- **Godoc/surface area.** The exported API must be documented to the same
  standard as the internal packages. Public docs are a commitment.
- **Versioning pressure.** The root module version (currently unpinned; `v0.0.0`
  in practice) must adopt semver. `v0.x.y` allows breaking changes; `v1.0.0`
  locks the public API.

### Migration path for `cmd/san`

Once `pkg/san` is stable, `cmd/san/main.go` can be refactored to use it:

```
Before:
  cmd/san → internal/app → internal/agent → internal/llm → internal/core

After:
  cmd/san → internal/app → pkg/san → pkg/llm → internal/llm → internal/core
```

This is a non-breaking internal refactor that proves the SDK works for the
primary use case (CLI agent) before external consumers depend on it.

## Implementation Plan

### Phase 1: LLM Client SDK (`pkg/llm`) — ~2 days

1. Create `pkg/llm/` directory.
2. Define public types: `Provider`, `Client`, `CompletionOptions`,
   `InferRequest`, `InferResponse`, `Chunk`, `StreamChunk`, `ModelInfo`,
   `Message`, `ToolSchema`, `ToolCall`.
3. Implement `NewProvider()` — thin wrapper around `internal/llm` registry.
4. Implement `Client` — thin wrapper around `internal/llm.Client`.
5. Write godoc examples.
6. Add tests that exercise all nine providers with mock backends.

### Phase 2: San Agent SDK (`pkg/san`) — ~3 days

1. Create `pkg/san/` directory.
2. Define public types: `Agent`, `Event`, `Result`, `AgentOption`.
3. Implement `New()` — thin wrapper around `internal/agent` construction.
4. Implement `Run()`, `ThinkAct()`, `Inbox()`, `Events()`.
5. Write godoc examples (standalone agent, agent with tools).
6. Add integration tests with a mock LLM.

### Phase 3: Dogfood & Polish — ~2 days

1. Refactor `cmd/san` to use `pkg/san` for agent construction.
2. Update `docs/packages/` with `pkg-llm.md` and `pkg-san.md`.
3. Update `reference/package-map.md` to include `pkg/` packages.
4. Write a migration guide for early adopters.

## References

- [`docs/architecture.md`](../../architecture.md) — system-level architecture overview.
- [`internal/core/agent.go`](../../../internal/core/agent.go) — `Agent` interface contract.
- [`internal/core/llm.go`](../../../internal/core/llm.go) — `LLM` interface contract.
- [`internal/llm/types.go`](../../../internal/llm/types.go) — `Provider` interface and types.
- [`internal/llm/llm.go`](../../../internal/llm/llm.go) — `Client` implementation.
- [`internal/agent/build.go`](../../../internal/agent/build.go) — agent construction.
- [ADR-0001: Layered package architecture](./0001-layered-package-architecture.md) — layer model this design extends.
