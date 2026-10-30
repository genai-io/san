# PROP-0004: ACP agent entrypoint

Draft — updated 2026-10-29. SDK typed endpoint implemented; San entrypoint pending.

## Scope

Add opt-in `san acp` without changing native TUI/print behaviour. Design for San's
full capability set; foreground-first delivery is a milestone, not a permanent
feature cut. Each release states what is available, extended or safely unavailable.

Keep one active root session per process initially; child tasks remain distinct.
TUI-over-ACP, remote hosting and multi-client control remain out of scope.

## Architecture

Target flow; the shared execution boundary and ACP adapter are not implemented:

```text
San TUI ── native calls ─────┐
                            ├─ Shared execution ── agent.Runtime ── sdk-go
External client ── ACP ──────┘
```

- `sdk-go/pkg/acp`: transport, wire types and typed endpoint with ordered admission/cancel.
- `internal/interaction`: request queue, lifetime/owner checks and cancellation replies.
  TUI display, authorization and audit ordering remain in `internal/app`.
- Shared input and turn lifecycle: pending; package boundaries remain to be determined.
  Compose existing services without a second engine, Bubble Tea or ACP types.
- ACP adapter: pending; capability negotiation, projections and extensions,
  without duplicate authorization, persistence or task scheduling.

Native transcripts remain authoritative. Extract shared behaviour incrementally;
shared feature packages must not import `internal/app`. The interaction extraction
is not a complete shared execution layer.

## Capability map

Target mappings, not implemented guarantees. Pin [ACP v1 schema-v1.26.0](https://github.com/agentclientprotocol/agent-client-protocol/tree/e2d83349bc9e28a4059ea3acefa1c91c966f74e2/schema/v1);
prefer its standard/optional surfaces before extensions or draft features.

| San capability | ACP surface / retained behaviour |
|---|---|
| Text, images, file references | Content blocks and resource links; negotiate richer inputs and preserve block order |
| Tools, Bash, diffs, large output | Tool updates, locations, diff/resource content; San executes, bounds previews and exposes full output safely |
| Main/worker approvals | `session/request_permission`; San validates scopes, audits before execution and keeps worker grants once-only |
| Questions / interactive Bash | Negotiated elicitation or an interaction extension; retain request/source identity; secrets require a separate safe channel, never chat/forms |
| History, fork, rename | Standard new/load/resume/list/delete/close where supported; extensions for missing operations; restore native state without granting bypass |
| Models, modes, personas, skills | Config options and executable command discovery; preserve native defaults and validate legacy-client mappings |
| Subagents, tasks, workflows | Standard tool/plan summaries plus task extensions for topology, output, messaging, Stop/Kill and continuation; a plan is not a scheduler |
| Cron, AutoPilot, groups, self-learning | Existing backend services/settings; commands/extensions for controls, explicit idle/stop/disconnect ownership |
| Memory, compaction, tokens/cost | Backend context policy; negotiated compaction/usage updates and commands; detailed diagnostics stay San-owned |
| MCP, plugins, hooks, trust | Existing native loading, scoped client MCP config and gated management commands; client settings never silently replace San policy |
| Recap, side questions, queued input | Separate command/queue operations; preserve whether input enters history or wakes main; layout/scrollback stay client-owned |

Extensions use a versioned `genai-io/san` capability in `_meta` and `_san/` methods,
only after agreement. Standard summaries stay readable without an extension UI;
missing controls are reported, not granted or disguised as user prompts.
Carry session, turn, tool-call, task, child-session, attempt and interaction IDs as
distinct scoped identities. Task actions revalidate the current attempt; continuing
asks main to choose a safe action instead of directly relaunching a worker.

## Flow and invariants

`initialize` → `session/new` or `session/load` → `session/prompt` →
`session/update` and approval requests → prompt result.

- One event owner applies shared input/hooks/context and durable effects before
  projection; keep native history and historical diffs, reject untrusted setup.
  Publish new-session commands only after its response; separate failed retries.
- Admit prompts in wire order before dispatch; reject overlap without cancelling
  the current turn. Early cancels and stale replies must not target a successor.
- `optionId`, not `allow_always`, selects the exact grant scope; validate/audit
  once before waking the original caller. Unsupported interactions fail closed.
- Prompt cancel settles after unwind; hold queued input, background-to-model delivery
  and wakeups until user continuation. Task updates stay visible; independent tasks
  keep running. Task Stop/Kill and session close/disconnect are separate operations.
- Close/disconnect stops owned work and releases resources; no detached execution.
  Load replays history; resume does not. Reconcile live attempts only within a
  surviving host; restart restores persisted state, not live checkpoints or approvals.
- Protocol-only stdout, bounded queues/previews and safe output references;
  blocked readers must not prevent cleanup. Never export credentials/signatures.

Validate baseline, optional-capability and San-extension clients separately.
Zed/Orca compatibility requires real tests; Orca launch registration is separate.

## Implementation references

Informative patterns, not a promise that every client supports their extensions:

- [OpenCode](https://github.com/anomalyco/opencode/tree/6aedf36ca307f97919f39d655de13b3ad5ccd24f/packages/cli/src/acp): native-runtime facade, child-scoped tool IDs, load/replay and post-response command publication.
- [Claude adapter](https://github.com/agentclientprotocol/claude-agent-acp/blob/0ce42f2c312d190f78024d8b4b1ae5f00d9e5939/src/tests/native-subagents.test.ts): distinct child lifecycles across reused task IDs and late events.
- [Codex adapter](https://github.com/zed-industries/codex-acp/blob/296069e841634cd4bb9bc4515602d836e49231ec/src/thread.rs#L2412-L2523): exact approval option scopes, structured diffs and negotiated terminal display.
- [Orca dialects](https://github.com/stablyai/orca/tree/v1.4.241/src/main/acp/acp-dialects): question, child-stop and background-kill gaps require explicit client support; do not copy vendor wire quirks.

[Implementation plan](../../../notes/active/acp-agent-entrypoint.md) ·
[ACP extensions](https://agentclientprotocol.com/protocol/v1/extensibility)
