# Writing a Subagent

A subagent is a markdown-defined **agent type** — a system prompt + tool
subset + permission mode that the foreground agent can spawn via the
`Agent` tool. Subagents run in parallel and report results back through
the tool result.

For the system-level design see [`packages/subagent.md`](../packages/2-feature/subagent.md)
and [`concepts/extension-model.md`](../concepts/extension-model.md).

## Where to Put It

| Scope | Path |
|---|---|
| Project | `<project>/.san/agents/<name>.md` |
| User | `~/.san/agents/<name>.md` |
| Claude-compat | `<project>/.claude/agents/<name>.md`, `~/.claude/agents/<name>.md` |

Project overrides user overrides Claude-compat by `name`.

## Minimal Example

`./.san/agents/test-runner.md`:

```markdown
---
name: test-runner
description: Run the test suite and surface failures
allow_tools: [Bash, Read, Grep]
mode: bypass
isolation: none
---

You are a test runner. Your job is to:

1. Detect the project's test command from package.json / Makefile / go.mod.
2. Run it. Capture stdout/stderr.
3. If failures exist, summarize the failing tests with file:line and
   one-line reasons. If everything passes, say "all green".

Be terse. No code suggestions — that is the parent agent's job.
```

## Frontmatter Fields

| Field | Required | Purpose |
|---|---|---|
| `name` | yes | Subagent type identifier; used in the `Agent` tool's `subagent_type` field. |
| `description` | yes | Shown in selectors and used by the foreground model to decide when to spawn this agent. |
| `allow_tools` | no | Restrict the subagent's tool set (nil = all tools). Aliases accepted: `allowed-tools`, and Claude Code's `tools`. |
| `deny_tools` | no | Tools (or `Tool(pattern)` rules) removed regardless of mode. |
| `mode` | no | Permission mode: `default`, `explore`, `edit` (alias of `acceptEdits`), `bypassPermissions`. Alias accepted: `permission-mode`. See below. |
| `isolation` | no | `none` (default) or `worktree` — see below. A per-spawn `isolation` argument overrides this. |
| `model` | no | Pin a model for this subagent; otherwise inherits the parent's. An alias (`opus`/`sonnet`/`haiku`) or bare id uses the parent's provider; `vendor/model` (e.g. `deepseek/deepseek-v4`) routes to another **connected** provider. |
| `max-steps` | no | Cap on LLM inference steps (default 100). |
| `skills` | no | Skill names whose bodies are preloaded into the agent's charter. |

Canonical keys win when both a canonical key and its alias are present.

## Cross-Provider Models

The `model:` field lets each subagent run on the model that fits its role —
using only frontmatter, with no router or extra config. A `vendor/model` value
routes the agent to another **connected** provider:

| Agent | `model:` | Why |
|---|---|---|
| `planner` | `anthropic/claude-opus-4-7` | strongest reasoning for decomposition |
| `coder` | `deepseek/deepseek-v4` | cheap, fast bulk implementation |
| `reviewer` | `anthropic/claude-sonnet-4-6` | independent review on a *different* model |

`model:` accepts:

- `inherit` (or empty) — the parent conversation's model.
- an alias (`opus` / `sonnet` / `haiku`) or a bare id — served by the parent's
  provider.
- `vendor/model` (e.g. `deepseek/deepseek-v4`) — routed to another **connected**
  provider.

The `vendor` names the model family, not the serving platform: auth comes from
how you connected that vendor, so Claude-on-Vertex is still `anthropic/…`. A
role whose vendor isn't connected fails with a clear "provider not connected"
error instead of silently falling back to the parent's model.

No policy engine is needed: the foreground agent picks which agent to spawn from
each `description` / `when-to-use`, so choosing the agent chooses the model.

## Permission Mode

Subagents have no UI, so permission prompts cannot be shown. The mode
controls what happens when a tool call would normally `ask`:

| Mode | Behavior |
|---|---|
| `explore` | Read-only: mutating tools are denied and hidden from the schema list. |
| `default` | Reads auto-allow; `ask` collapses to `deny`, so mutations are blocked unless an `allow_tools` rule covers them. |
| `edit` (= `acceptEdits`) | Edit/Write auto-allow; other gated tools (e.g. unrestricted Bash) are denied. |
| `bypassPermissions` | Everything allowed except bypass-immune safety checks. Trusted agents only — no human in the loop. |

The `Agent` tool is never available to subagents — the agent model is flat,
and only the main conversation spawns subagents. `SendMessage` (a subagent
reporting to `"main"`) follows the ordinary mode pipeline.

See [`concepts/agent-communication.md`](../concepts/agent-communication.md)
for the message queue and [`concepts/permission-model.md`](../concepts/permission-model.md)
for the full decision pipeline.

## Worktree Isolation

`isolation: worktree` creates a `git worktree` under
`<project>/.git/agent-worktrees/<random>/` and runs the subagent there.
The parent's working tree is untouched. On exit the worktree is removed
**only if it is clean**; uncommitted changes preserve it, and the agent's
result includes `[worktree preserved] … <path>` so the parent can review,
merge, or discard the work.

Use this when you want a subagent to run experiments that may dirty the
tree (build artifacts, codegen, refactors) without polluting the
foreground session.

## How the Parent Spawns It

The foreground agent calls the `Agent` tool with:

```json
{
  "subagent_type": "test-runner",
  "description": "Run the unit tests for the auth package",
  "prompt": "Focus on internal/auth/*_test.go and report failures."
}
```

San:

1. Looks up `test-runner` in the subagent registry.
2. Builds a `core.Agent` with the subagent's charter and tool subset.
3. Runs it in the spawning turn (foreground), or as a `task.AgentTask`
   with `run_in_background: true`.
4. Returns the final aggregated result to the parent agent — as the tool
   result (foreground) or a `<task-notification>` on completion
   (background).

## Talking to a Running Worker

A background subagent registers with the broker for its whole run — see
[`concepts/agent-communication.md`](../concepts/agent-communication.md) for
the model:

- **Steer**: `SendMessage(to=<task id>, message)` posts into the running
  subagent's inbox; it reads the message at its next step. Best-effort — a
  subagent that has finished, or is deep in a long tool call, may not see it.
- **Report to main**: inside a background subagent,
  `SendMessage(to="main", message)` sends an interim note without ending the
  run. The subagent's *final* answer returns on its own as a completion —
  don't use `SendMessage` for it.
- **Stop**: `TaskStop` cancels a run (status `stopped`, distinct from
  `failed`). Subagents are one-shot — there is no resume; spawn a fresh one to
  continue a line of work.

## Trying It

1. Save the agent file.
2. Restart `san`.
3. Ask the foreground model something that should trigger spawning:
   "use the test-runner agent to check whether tests pass".
4. Watch the task panel for the subagent's progress; the final result
   appears as a tool result in the parent's conversation.

## Common Pitfalls

- **No prompt argument.** If `prompt` is missing, the subagent has no
  instructions beyond its system prompt. Always pass a prompt.
- **`allow_tools` too narrow.** Forgot `Bash`? The subagent can't run
  anything — and a whitelist without `SendMessage` also removes reporting
  to main.
- **`bypassPermissions` on a write-class subagent.** Verify carefully
  — the subagent has no human in the loop.
- **Same name in several locations.** Priority is project `.san` > user
  `~/.san` > project `.claude` > user `~/.claude` > plugins; the
  higher-priority definition wins.

## See Also

- [`concepts/agent-communication.md`](../concepts/agent-communication.md) —
  how the main conversation and its subagents message each other.
- [`packages/subagent.md`](../packages/2-feature/subagent.md) — registry +
  executor design.
- [`packages/agent.md`](../packages/2-feature/agent.md) — foreground agent
  lifecycle.
- [`concepts/extension-model.md`](../concepts/extension-model.md).
- [`concepts/permission-model.md`](../concepts/permission-model.md).
