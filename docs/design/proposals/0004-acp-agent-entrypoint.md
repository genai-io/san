# PROP-0004: ACP agent entrypoint

Draft — 2026-10-26. Transport kernel prototyped in `sdk-go`; San entrypoint pending.

## Scope

Add opt-in `san acp`: ACP v1 over stdio for external clients. Support foreground
coding, text/resource links, tool results/diffs, approvals, session loading and
stdio MCP. Keep existing TUI and print behavior unchanged.

Defer TUI-over-ACP, background/service launches, autonomous orchestration, remote
transport and multi-session hosting. Enforce these limits at execution, not just
by hiding controls.

## Architecture

```text
San TUI ── native calls ─────┐
                            ├─ Session host ── agent.Runtime ── sdk-go
External client ── ACP ──────┘
```

- Proposed `internal/app/agenthost`: shared session lifetime, input preparation,
  lifecycle effects and interaction policy; no Bubble Tea or ACP types.
- Proposed `internal/app/acp`: protocol dispatch and safe event projection;
  no duplicate authorization, persistence or execution logic.
- Reuse existing services; generic ACP helpers live in `sdk-go/pkg/acp`.
  Native transcripts stay authoritative; neither new package imports root `app`.

## Flow and invariants

`initialize` → `session/new` or `session/load` → `session/prompt` →
`session/update` and approval requests → prompt result.

- Use workspace-scoped configuration; missing setup/trust fails explicitly.
  Loading history never restores bypass grants.
- One event consumer records/applies lifecycle effects before projection.
  Retries must not merge abandoned output into the final answer.
- Validate approval ownership/scope; audit before waking the gate. Unsupported
  interactions fail closed; secrets never fall back to chat/forms.
- Cancel settles at the real turn boundary. EOF/failure cancels pending work,
  stops owned services and releases the writer. Reconnect loads history, not
  process-local worker checkpoints.
- Protocol-only stdout, diagnostics on stderr. Bound frames/output queues;
  blocked readers must not prevent cancellation or cleanup.

Validate one session/prompt per process with Zed first. Orca requires separate
launch registration. No capability is advertised before validation.

[Implementation plan](../../../notes/active/acp-agent-entrypoint.md) ·
[ACP v1](https://agentclientprotocol.com/protocol/v1/overview) ·
[Zed custom agents](https://zed.dev/docs/ai/external-agents.md#custom-agents)
