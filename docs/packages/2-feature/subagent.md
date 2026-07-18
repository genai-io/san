---
package: github.com/genai-io/san/internal/subagent
layer: feature
---

# subagent

Registry of custom agent **types** (markdown-defined personas with their
own system prompt, tool subset, and permission mode) plus the **Executor**
that spawns them as background `core.Agent` instances from within the
main agent's tool loop.

## Purpose

Where [`packages/agent.md`](agent.md) owns the *foreground* agent session,
this package owns the *background* agents the foreground spawns via the
Agent tool. Each background agent runs in its own goroutine with its own
inbox/outbox and isolated tool/permission set while sharing the parent's
working directory; its final result flows back into the foreground
conversation as a tool result.

## Contract

The package exposes the concrete `*Registry` directly. The four
production caller sites use four different subsets of its surface, so
no producer-side role interface earns its keep — TEMPLATE Rule 3.

| Caller | Methods used |
|---|---|
| `cmd/san agent` | `Get` (CLI argument validation) |
| TUI view | `ListConfigs` (color enumeration) |
| Agent build site | `PromptSection` (twice) |
| TUI selector adapter | full surface — `ListConfigs`, `IsEnabled`, `SetEnabled`, `GetDisabledAt` for the `/agent` menu |

Executor construction goes through the package-level `NewExecutor`
free function (in `executor.go`), not a method on the registry. The
free function takes `hook.Handler`, keeping subagent decoupled from
the concrete `*hook.Engine`.

```go
package subagent

// Registry is an opaque handle to the agent type registry. The type is
// exported so callers can hold and pass *Registry values; all fields
// are unexported so internal state is reached only through methods.
type Registry struct { /* internal fields */ }

// Query
func (r *Registry) ListConfigs() []*AgentConfig
func (r *Registry) Get(name string) (*AgentConfig, bool)
func (r *Registry) IsEnabled(name string) bool

// State mutation (used by the TUI selector adapter)
func (r *Registry) SetEnabled(name string, enabled bool, userLevel bool) error
func (r *Registry) GetDisabledAt(userLevel bool) map[string]bool

// System prompt
func (r *Registry) PromptSection() string

// Loader bootstrapping
func (r *Registry) Register(config *AgentConfig)
func (r *Registry) InitStores(cwd string) error

// Executor construction (package-level free function)
func NewExecutor(provider llm.Provider, cwd, parentModelID string, hooks hook.Handler) *Executor

// Package-level access
func Initialize(opts Options) error
func Default() *Registry
func SetDefaultRegistry(r *Registry)  // test-only
func ResetDefaultRegistry()           // test-only
```

## Internals

- `Registry` (`registry.go`) — `AgentConfig` map keyed by name, plus
  enable state stores (user + project).
- `Executor` (`executor.go`) — spawns a `core.Agent` for one subagent
  invocation, manages its lifecycle, drains its outbox, and returns the
  aggregated result.
- `executor_prompt.go` / `executor_run.go` / `executor_session.go` —
  split executor concerns (prompt assembly, run loop, session attribution).
- `loader.go` — reads markdown agent definitions from
  `~/.san/agents/`, `<project>/.san/agents/`, plus plugin paths.
- `match.go` — name matching for the Agent tool's `agent_type` parameter.
- `progress_tools.go` — pseudo-tools the subagent emits to surface
  progress to the parent agent.

## Lifecycle

- Construction: `Initialize(Options{CWD, PluginAgentPaths})` loads
  definitions, initializes state stores.
- Per-invocation: `NewExecutor(provider, cwd, model, hookEngine)` →
  `Executor.Run(ctx, params)` spawns a `core.Agent`, blocks until end of
  turn, returns aggregated `Result`.
- Concurrency: multiple executors may run in parallel; the registry is
  RWMutex-protected.

## Tests

```
internal/subagent/executor_test.go      — end-to-end run scenarios.
internal/subagent/lazy_loading_test.go  — config files read on demand.
internal/subagent/scenarios_test.go     — common invocation shapes.
```

## See Also

- Code: `internal/subagent/`
- Parent agent: [`packages/agent.md`](agent.md)
- Spawning tool: [`packages/tool.md`](tool.md) (the Agent tool)
- Layer: `feature`
