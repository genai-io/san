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

Provably read-only Bash invocations short-circuit to allow at the
mode-default step (`setting.IsReadOnlyBashCommand`): every command in the
chain must be on the read-only list (`rg`, `grep`, `find`, `ls`, read-only
`git`, …) with no output redirection, substitution, or env-var prefix.
This replaces the retired Grep/Glob tools — search runs through Bash
without approval prompts, in every mode including explore. Deny/ask rules
and the safety checks still run first and override it. The safety checks
are layered: the circuit breaker (a recursive removal of the filesystem
root or the home directory) holds in every mode, bypass included; the
confirmation tiers — unrecoverable (destructive commands, sensitive paths,
data exfiltration) and recoverable (work-discarding git,
out-of-working-dir writes) — are skipped by `bypassPermissions` mode.

## Subagent Permission Resolution

The foreground and subagent gates both use `setting.ModeDefault` for
mode-specific default decisions, but otherwise have separate pipelines:
foreground requests may use settings, session rules, hooks, and the approval
bridge, while subagents use only their effective mode and deny requests that
would require a user prompt.

- Foreground: yes. `agent.PermissionBridge` synchronously waits for the
  TUI approval, then routes the answer back into the running tool call.
- Subagent: no. There's no user attached to the subagent's loop, so `ask`
  collapses to `deny`. What remains is the mode's own auto-allow surface:
  - `explore` — reads only; mutations are denied outright.
  - `default` — reads auto-allow; everything that would ask is denied.
  - `acceptEdits` (spelled `edit` on the Agent tool) — Edit/Write
    auto-allow; other gated tools are denied.
  - `bypassPermissions` — everything allowed after the root/home-removal
    circuit breaker; parent-only tools stay blocked.

Both gates share the same mode table (`setting.ModeDefault`); the subagent
side only swaps "prompt the user" for "deny".

One subagent-specific rule composes with the pipeline:

- **Flat spawning** — only the main conversation spawns subagents. The `Agent`
  tool is parent-only, so a subagent never sees its schema; there is no
  spawn-permission logic on the subagent side at all. `SendMessage` (main ↔ a
  running subagent) follows the ordinary mode pipeline like any other tool.
  See [`packages/broker.md`](../packages/2-feature/broker.md).

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
