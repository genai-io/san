# Permission Model

Every tool call passes through one gate: `setting.HasPermissionToUseTool`.
This page documents the inputs, the decision pipeline, and how the
foreground TUI, subagents, and plan-mode differ.

For the Claude-Code-compatible rule syntax see
[`reference/claude-permission-compat.md`](../reference/claude-permission-compat.md).

## Vocabulary

| Term | Meaning |
|---|---|
| **Behavior** | The intent: `allow`, `deny`, or `ask`. |
| **Decision** | Behavior + reason + suggested rule edits. The runtime returns one of these per call. |
| **Mode** | Session-wide policy: `default`, `acceptEdits`, `bypassPermissions`, `plan`. |
| **Rule** | A pattern matched against `toolName + args`. E.g. `Bash(git status:*)`, `Write(./src/**)`. |
| **Session permissions** | Per-session rules accumulated from hook responses and approval modals. Reset on session end. |

## Sources of Authority

Five sources are consulted in this order; the first to produce a
non-`ask` behavior wins:

1. **Settings rules** — `settings.json` `permissions.allow / deny / ask`,
   merged across user and project tiers.
2. **Session permissions** — rules added during the run (via approval
   modals or hook updates).
3. **Hook responses** — `PermissionRequest` hook (if any) may force
   `allow` or `deny`, or rewrite the request.
4. **Mode policy** — `bypassPermissions` forces allow; `plan` forces deny
   for any write-class tool.
5. **Default** — `ask`.

The pipeline lives in `internal/setting/permission.go`. Bash gets special
treatment: `bash_ast.go` parses the command and matches per-argv patterns
(`Bash(git status:*)` allows `git status -uall` but not `git push`).

## Single Bit of Difference: "Can We Prompt?"

The decision pipeline is **identical** for the foreground agent and for
subagents. The single point of divergence is: when behavior is `ask`, can
we surface a modal to the user?

- Foreground: yes. `agent.PermissionBridge` synchronously waits for the
  TUI approval, then routes the answer back into the running tool call.
- Subagent: no. There's no user attached to the subagent's loop.
  Subagents resolve `ask` according to their `permission_mode`:
  - `bypassPermissions` (the explore agent) — treat as allow
  - `plan` — treat as deny + emit a "would have asked" record
  - default — treat as deny

This is implemented as one flag on the request, not by duplicating logic
on the subagent side.

## Plan Mode

A read-only conversation. Write-class tools (`Write`, `Edit`,
`NotebookEdit`, certain Bash patterns) are auto-denied with a synthetic
reason `"plan mode: read-only"`. The user can exit plan mode with
`/plan-off` or by approving an `ExitPlanMode` tool call.

Plan mode is a **policy filter**, not a separate code path. The same
`HasPermissionToUseTool` returns `deny(plan-mode)` early.

## Hooks Can Mutate Permissions

The `PermissionRequest` hook fires before the modal is shown (or before
the auto-deny in subagent mode). The hook can:

- Force `allow` or `deny` for this single call.
- Append session-scope rules to be applied to subsequent calls.
- Switch the mode (e.g. flip to `acceptEdits` for the rest of the
  session).
- Rewrite the tool args (e.g. canonicalize a path).

See [`packages/hook.md`](../packages/2-feature/hook.md) for the request/response
shape, and `PermissionUpdate` in `internal/hook/types.go` for the
mutation payload.

## Implementation Pointers

- Decision gate: `internal/setting/permission.go` → `HasPermissionToUseTool`.
- Rule parser + Bash AST: `internal/setting/bash_ast.go`.
- Approval modal flow: `internal/agent/permission.go` (`PermissionBridge`).
- Subagent permission resolution: `internal/subagent/executor.go`.
- Hook integration: `internal/hook/engine.go` → `getPermissionRequestOutcome`.

## See Also

- Packages: [`setting`](../packages/2-feature/setting.md), [`tool`](../packages/2-feature/tool.md), [`agent`](../packages/2-feature/agent.md), [`subagent`](../packages/2-feature/subagent.md), [`hook`](../packages/2-feature/hook.md)
- Compatibility note for Claude Code rule files: [`reference/claude-permission-compat.md`](../reference/claude-permission-compat.md)
