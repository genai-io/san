# Permission Model

Every tool call passes through one gate: `setting.HasPermissionToUseTool`.
This page documents the inputs, the decision pipeline, and how the
foreground TUI and subagents differ.

For the Claude-Code-compatible rule syntax see
[`reference/claude-permission-compat.md`](../reference/claude-permission-compat.md).

## Vocabulary

| Term | Meaning |
|---|---|
| **Behavior** | The intent: `allow`, `deny`, or `ask`. |
| **Decision** | Behavior + reason + suggested rule edits. The runtime returns one of these per call. |
| **Mode** | Session-wide policy. See the mode table below. |
| **Rule** | A pattern matched against `toolName + args`. E.g. `Bash(git status:*)`, `Write(./src/**)`. |
| **Session permissions** | Per-session rules accumulated from hook responses and approval modals. Reset on session end. |
| **Confirmation tier** | Why a call needs a human: *unrecoverable* (real destruction, sensitive paths, data exfiltration) or *recoverable* (work-discarding git, out-of-working-dir writes). Only the recoverable tier may be delegated to the judge. |

## Modes

`setting.OperationMode` (`internal/setting/settings.go`) has six values:

| Mode | `String()` | Behavior |
|---|---|---|
| `ModeNormal` | `normal` | Safe tools auto-allow; everything else prompts. |
| `ModeAutoAccept` | `accept edits` | Edit/Write auto-allow; other gated tools prompt. |
| `ModeAutoPilot` | `autopilot` | Edits auto-allow; every other gated call becomes a **reviewable** prompt the judge may answer. |
| `ModeBypassPermissions` | `YOLO` | Allow everything except deny rules and the circuit breaker. |
| `ModeDontAsk` | `don't ask` | Coerce `ask` → `deny`; never prompt. |
| `ModeReadOnly` | `read-only` | Safe tools only; everything else denied. |

The first four make up the user-facing cycle: `cycleModes` steps through
normal / accept-edits / autopilot, and `cycleModesWithBypass` adds YOLO
mode. `ModeDontAsk` and `ModeReadOnly` are entered programmatically
(headless runs, the subagent explore mode), not by cycling.

Which of the two cycles applies is the `allowBypass` user setting, toggled
from `/settings › permissions`. It is opt-out: unset means YOLO mode is in
the cycle. Locking it removes that step, downgrades a `bypassPermissions`
defaultMode to normal at startup, and drops a session already running in
YOLO mode back to normal.

That gate covers the foreground cycle only. `allowBypass` is read in two
places — `OperationMode.NextWithBypass` and `env.ApplyDefaultPermissionMode`
— and the subagent path (`subagent.NormalizePermissionMode`) is not one of
them, so an agent declaring `mode: bypassPermissions` runs unrestricted
even while the gate is locked.

## Decision Pipeline

`HasPermissionToUseTool` walks nine steps in order; the first to produce a
decision wins. The step list here mirrors the comment block above the
function in `internal/setting/permission.go` — keep the two in sync.

1. **Deny rules** — `permissions.deny`, merged across user and project tiers.
2. **Circuit breaker** — a recursive removal of the filesystem root or the
   home directory. The one check no mode may skip, bypass included.
3. **BypassPermissions mode** — allow everything that got past 1 and 2.
4. **Confirmation checks** (skipped by bypass) — the unrecoverable tier
   (destructive bash, sensitive paths, data exfiltration; never
   judge-reviewable) and the recoverable tier (work-discarding git,
   out-of-working-dir writes; the AutoPilot judge may weigh the git case).
5. **Ask rules** — `permissions.ask`. An explicit ask outranks any session
   grant, so "allow for this session" cannot silence it.
6. **Session permissions** — rules accumulated during the run from approval
   modals and hook updates.
7. **Allow rules** — `permissions.allow`.
8. **Mode default** — `setting.ModeDefault`, the mode table above. This is
   where AutoPilot marks a prompt `Reviewable`.
9. **Headless / DontAsk coercion** — `ask` becomes `deny` when nobody can
   answer.

Hooks sit on either side of this gate rather than inside it:

- A **`PreToolUse`** hook runs before the gate. It can block the call,
  rewrite the input, force a prompt, or answer `allow`. An `allow` only
  waives the routine prompt — `setting.ResolveHookAllow` re-checks it, so a
  deny rule, the circuit breaker, either confirmation tier or an explicit
  ask rule still sends the call through the gate.
- A **`PermissionRequest`** hook fires when a call has reached a prompt.

The pipeline lives in `internal/setting/permission.go`. Bash gets special
treatment: `bash_ast.go` parses the command and matches per-argv patterns
(`Bash(git status:*)` allows `git status -uall` but not `git push`). Allow
rules match the executable as written, so `Bash(git:*)` does not cover
`./x/git`, and a command that expands at run time (`$VAR`, `$(...)`, `<(...)`,
`$((...))`) matches no narrow allow rule. Deny rules and the safety checks
match by basename and look inside substitutions, so neither form slips past
them.

Provably read-only Bash invocations short-circuit to allow at the
mode-default step (`setting.IsReadOnlyBashCommand`): every command in the
chain must be on the read-only list (`rg`, `grep`, `find`, `ls`, read-only
`git`, …), named without a path, with no output redirection, substitution, or
env-var prefix.
This replaces the retired Grep/Glob tools — search runs through Bash
without approval prompts, in every mode including explore. Deny/ask rules
and the safety checks still run first and override it. The safety checks
are layered: the circuit breaker (a recursive removal of the filesystem
root or the home directory) holds in every mode, bypass included; the
confirmation tiers — unrecoverable (destructive commands, sensitive paths,
data exfiltration) and recoverable (work-discarding git,
out-of-working-dir writes) — are skipped by `bypassPermissions` mode.

Git global path options (`-C`, `--git-dir`, `--work-tree`) are parsed with their
operands before classifying the subcommand. Unknown options, configuration and
executable overrides, and unsafe output/helper flags do not qualify as reads.
`symbolic-ref` is read-only only for a one-ref query with known query flags;
two-ref writes and deletion are rejected. `reflog` permits the no-argument query
and explicit `show`, `exists`, or `list` modes, not metadata mutations such as
`expire`, `delete`, `drop`, or `write`. Use `reflog show` for queries with options.
These checks live in `bash_ast.go`; `bash_git_options_test.go` and
`bash_git_mutation_test.go` cover path options and metadata query/write forms.

## Subagent Permission Resolution

The foreground and subagent gates both use `setting.ModeDefault` for
mode-specific default decisions, but otherwise have separate pipelines:
foreground requests may use settings, session rules, hooks, and the approval
bridge, while subagents apply their own `deny_tools`/`allow_tools` rules.
A live main-session UI can answer a subagent's ordinary `ask` decision; it cannot
override a backend `deny`.

- Main conversation: `agent.PermissionGate` waits for the UI, then routes the
  decision to the original tool call.
- Foreground and background subagents: `Executor.SetInteractions` binds the
  session UI. The backend forwards only ordinary prompts:
  - `explore` — reads only; mutations remain hard denials.
  - `default` — reads auto-allow; other ordinary actions require approval.
  - `acceptEdits` — Edit/Write auto-allow; other ordinary gated actions prompt.
  - `dontAsk` — prompting decisions remain denials.
  - `bypassPermissions` — ordinary actions skip approval after `deny_tools` and
    the root/home-removal circuit breaker; parent-only tools stay blocked.
- A subagent with no live UI fails a prompting tool call promptly. It never
  silently grants permission or waits on an unpolled channel.
- Existing worker safety/confirmation hard denials, read-only boundaries and
  constrained tool rules remain authoritative, including after a user approval.
- Worker approvals offer **Yes / No** for that invocation only. They do not
  add parent-session or persistent rules. The UI records the worker's identity,
  tool-call correlation, working directory and effective mode.
- Real `AskUserQuestion` requests still need an answer in YOLO. The parent's
  copilot does not invent answers to worker questions.

The UI owns one FIFO across approvals, questions and secret prompts. An answer
resumes exactly the suspended caller. Cancellation removes its request without
canceling independent background workers; session reset/close invalidates the
transport and all its outstanding requests. Stale answers cannot resolve a
successor request. Both gates retain `setting.ModeDefault` as their mode table.

One subagent-specific rule composes with the pipeline:

- **Flat spawning** — only the main conversation spawns subagents. The `Agent`
  tool is parent-only, so a subagent never sees its schema; there is no
  spawn-permission logic on the subagent side at all. `SendMessage` (main ↔ a
  running subagent) follows the ordinary mode pipeline like any other tool.
  See [`packages/broker.md`](../packages/2-feature/broker.md).

## The Gray Zone: Auto-Review

In `ModeAutoPilot`, a call that would prompt is offered to a review agent
before it is offered to the user. One flag decides which calls those are,
and the reviewer has no say in it:

- `PermissionDecision.Reviewable` (`internal/setting/permission.go`) marks a
  prompt the judge may weigh.
- `setting.ModeDefault` sets it **only** for the AutoPilot non-edit default
  (step 8). Nothing else in the pipeline sets it.
- `PermissionGate.Check` (`internal/agent/permission.go`) offers a reviewable
  prompt to the judge; `allow` short-circuits, anything else falls through to
  the human prompt.
- The judge fails closed: `PermReviewFunc` must return the zero value on any
  error, so a broken or slow reviewer escalates rather than approves.

The load-bearing safety property is which prompts never reach the judge.
`ConfirmationTier` splits the confirmation reasons in two, and only the
**recoverable** tier is reviewable. The unrecoverable tier — real
destruction, sensitive paths, data exfiltration — and the circuit breaker
stay the human's call in every mode.

The reviewer itself lives in `internal/reviewer/`.

## Hooks Can Mutate Permissions

In the main conversation, the `PermissionRequest` hook fires before the modal
is shown. The hook can:

- Force `allow` or `deny` for this single call.
- Append session-scope rules to be applied to subsequent calls.
- Switch the mode (e.g. flip to `acceptEdits` for the rest of the
  session).
- Rewrite the tool args (e.g. canonicalize a path).

See [`packages/hook.md`](../packages/2-feature/hook.md) for the request/response
shape, and `PermissionUpdate` in `internal/hook/types.go` for the
mutation payload.

## PowerShell

Where the shell is PowerShell (the Windows default, or `SAN_SHELL`),
shell calls go to the `PowerShell` tool and are classified in PowerShell's own
terms — there is no PowerShell parser, so every judgment errs one way:

- **Read-only** runs without asking: one pipeline of read-only cmdlets
  (`Get-ChildItem`, `Get-Content`, `Select-String`, `Sort-Object`, …) or
  read-only `git`/`rg`, with nothing that could hide more — no `;`, `&&`, `||`,
  `&`, `(`, `{`, `$`, `@`, `[`, backtick, redirection or newline — and no UNC
  path.
- **Rules** name the exact command, or `PowerShell(name:args-glob)` like Bash's.
  An allow rule broader than the exact command covers a simple command only, so
  a prefix never waves through a chained one; deny and ask fire on any statement
  anywhere in the command.
- **Confirmation** — a recursive `Remove-Item` (any alias, any `-r…`
  abbreviation, `rd /s`), disk and partition cmdlets, scheduled tasks, services
  and elevation — is found anywhere in a statement, so wrapping it in
  `cmd /c` or `pwsh -c` does not hide it. A recursive removal of a drive root
  or the home directory trips the circuit breaker, even in bypass mode.
- A bare `Bash` or `PowerShell` in an allow, deny or `allow_tools` list names
  the shell tool, whichever the platform has. A pattern is its shell's syntax:
  `Bash(git status)` never applies to PowerShell.

## Implementation Pointers

- Decision gate: `internal/setting/permission.go` → `HasPermissionToUseTool`.
- Rule parser + Bash AST: `internal/setting/bash_ast.go`.
- Approval modal flow: `internal/agent/permission.go` (`PermissionGate`).
- Gray-zone judge: `internal/reviewer/`.
- Hook-allow invariant: `internal/setting/permission.go` → `ResolveHookAllow`,
  applied in `internal/tool/pretool_hook.go`.
- Subagent permission resolution: `internal/subagent/executor.go`.
- Hook integration: `internal/hook/executor.go` → `applyPermissionDecision`.

## See Also

- Packages: [`setting`](../packages/2-feature/setting.md), [`tool`](../packages/2-feature/tool.md), [`agent`](../packages/2-feature/agent.md), [`subagent`](../packages/2-feature/subagent.md), [`hook`](../packages/2-feature/hook.md)
- Compatibility note for Claude Code rule files: [`reference/claude-permission-compat.md`](../reference/claude-permission-compat.md)
