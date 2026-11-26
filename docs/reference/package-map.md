# Package Map

This map describes current package ownership and the layer each package
belongs to. It should match the code under `cmd/` and `internal/`. Layer
names are defined in `dependency-rules.md` — update both files together when
the assignment changes.

## Entrypoints

| Path | Layer | Responsibility |
| --- | --- | --- |
| `cmd/san` | `cmd` | Main CLI binary. Parses flags and dispatches to the interactive, print or protocol host. |

## Application Shell

`internal/app` groups host packages; the directory itself is not a Go package.

| Path | Layer | Responsibility |
| --- | --- | --- |
| `internal/app/tui` | `app` | Bubble Tea root model, session restore and event routing, including file watcher events, cron dispatch and async hook polling. |
| `internal/app/tui/transcript` | `app` | Conversation rendering state and agent outbox observation. |
| `internal/app/tui/input` | `app` | Text input, selectors, approvals, user-input flow. |
| `internal/app/ask` | `app` | User request lifetimes and permission resolution shared by TUI and ACP; no rendering or transport. |
| `internal/app/tui/widgets` | `app` | Shared terminal widgets, including input history (`widgets/history`) and completions (`widgets/suggest`). |
| `internal/app/acp` | `app` | ACP stdio host: native sessions, turns, permissions, extensions and workers, without the TUI. |
| `internal/app/ext` | `app` | Exposes plugin-provided extensions to hosts. |
| `internal/app/inspector` | `app` | Transcript inspector server, replay, stream, embedded UI. |
| `internal/app/hooks` | `app` | Binds hooks to sessions. |
| `internal/app/cli` | `app` | `cli.Print` and `cli.RunAgent` execute one-shot runs. CLI and ACP share provider/model selection in `internal/llm`; `cmd/san` emits startup diagnostics. |

## Core Contracts

| Path | Layer | Responsibility |
| --- | --- | --- |
| `internal/core` | `core` | Shared contracts for messages, tools, agents, sections, and system prompts. |

## Feature Packages

Agent, persistence, and orchestration:

| Path | Layer | Responsibility |
| --- | --- | --- |
| `internal/agent` | `feature` | Agent construction, permission adapter, and runtime lifecycle. |
| `internal/agent/mail` | `feature` | Routes addressed messages and task completions between agents; independent of the agent runtime. |
| `internal/aside` | `feature` | Focused model calls outside the main conversation, with request snapshots and session-owned recording. |
| `internal/reflect/memory` | `feature` | Repository memory updates, change validation, and note storage. |
| `internal/llm` | `feature` | The active connection, the provider registry, the model store, error classification, cost tracking, and the `vendor_*` adapter reaching every vendor through `genai-io/sdk-go`. |
| `internal/tool` | `feature` | Built-in tool schemas, registry, adapters, permission checks, execution. Subpackages hold the tool adapters, named after the capability they expose (`fs`, `agent`, `cron`, `skill`, `todo`, `web`, `ask`, `reviewskill`, `group`); `register` blank-imports them all to trigger registration; `perm` and `toolresult` are shared types. |
| `internal/session` | `feature` | Canonical project/session identity, typed events and native messages, request/state/display projections, assets, resume-directory validation, resume, fork and retention. |
| `internal/task` | `feature` | Background task storage and shutdown, bash and agent task execution, output storage. |
| `internal/todo` | `feature` | Agent to-do list state, background worker items, and the session-owned tracker bound to tool contexts. |
| `internal/subagent` | `feature` | Subagent registry, loading, matching, execution, storage, progress tools. |
| `internal/flow` | `feature` | Collaboration blueprints, validation, dependency scheduling, bounded loops/fan-out and checkpoint recovery. Nodes use the existing subagent executor; task detail stays native. |
| `internal/group` | `feature` | Groups of sessions on one machine: member files, per-member inboxes, online status from pid and process start time. |
| `internal/cron` | `feature` | Cron definitions, storage, service, loop. |
| `internal/reflect/recap` | `feature` | Return recap instructions and model-response handling; the TUI owns timing and display. |
| `internal/reflect/skill` | `feature` | Auto Skill review, proposal validation, and agent-owned skill changes. |

Extension surfaces:

| Path | Layer | Responsibility |
| --- | --- | --- |
| `internal/command` | `feature` | Slash command registry, built-ins, Markdown command loading. |
| `internal/skill` | `feature` | Skill registry, loading, lazy loading metadata. |
| `internal/plugin` | `feature` | Plugin compatibility, loading, installation, marketplace, integration. |
| `internal/mcp` | `feature` | MCP config, client, registry, tool catalog synchronization, caller and hook integration. |
| `internal/mcp/transport` | `feature` | MCP transport implementations. |
| `internal/hook` | `feature` | Hook registry, matcher, engine, executors, store. |

Configuration and supporting capabilities:

| Path | Layer | Responsibility |
| --- | --- | --- |
| `internal/setting` | `feature` | Settings loading, merge, permissions, workspace trust policy and state, operation mode, workdir, env. |
| `internal/setting/trust` | `feature` | Collects cross-feature trust findings for entrypoints; asks settings to trust clean workspaces for the session. |
| `internal/persona` | `feature` | Persona registry (system-prompt parts, skills, settings overlay), template, paths. |
| `internal/search` | `feature` | Search provider implementations and factory. |
| `internal/reminder` | `feature` | Runtime reminder queue and provider integration. |
| `internal/judge` | `feature` | LLM judge for gray-zone permissions and bash prompts. |
| `internal/image` | `feature` | Image handling + `core.Image` adapter; provisional — see note in `dependency-rules.md`. |

## Infrastructure Helpers

| Path | Layer | Responsibility |
| --- | --- | --- |
| `internal/infra/log` | `infrastructure` | Logging, request/response logs, development log paths. |
| `internal/infra/secret` | `infrastructure` | Secret storage helpers. |
| `internal/infra/filecache` | `infrastructure` | File restore/cache helpers. |
| `internal/infra/markdown` | `infrastructure` | Markdown frontmatter parsing. |
| `internal/infra/proc` | `infrastructure` | Cross-platform process-group / signal helpers (Unix/Windows variants) and the OS URL launcher. |
| `internal/infra/confdir` | `infrastructure` | Shared `.san` configuration directory naming, and scanning config folders through symlinks. |
| `internal/infra/atomicfile` | `infrastructure` | Temp-file-then-rename file replacement, so a reader never sees a half-written file. |
| `internal/autoupdate` | `infrastructure` | Replaces the running binary with a newer GitHub release by download-beside-and-rename; shared by `san update` and the TUI's background check. |

## Ownership Rule

New behavior should live in the package that owns the capability. Add a new
top-level `internal/` package only when the behavior has a distinct lifecycle,
state model, or dependency boundary that does not fit an existing package.
When adding a package, assign it a layer in this map and confirm the imports
match `dependency-rules.md`.
