# Writing a Subagent

A subagent is a markdown-defined **agent type** — a system prompt + tool
subset + permission mode that the foreground agent can spawn via the
`Agent` tool. Foreground subagents return a tool result; background ones notify
the main conversation when done.

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
allow_tools: [Bash, Read]
mode: bypass
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
| `description` | yes | Helps the foreground model decide when to spawn this agent. |
| `allow_tools` | no | Allowed tools. Defaults to all; aliases: `allowed-tools`, `tools`. |
| `deny_tools` | no | Removes tools or `Tool(pattern)` rules. |
| `mode` | no | `default`, `explore`, `edit` (`acceptEdits`), or `bypassPermissions`; alias: `permission-mode`. |
| `model` | no | Defaults to the parent's model. Aliases/bare IDs use its provider; `vendor/model` uses a connected provider or falls back to the parent. |
| `max-steps` | no | Maximum LLM steps; default: 100. Alias: `max_steps`. |
| `skills` | no | Skills preloaded into the agent charter. |

Canonical keys win when both a canonical key and its alias are present.

## Cross-Provider Models

The `model:` field selects a model for this subagent. `vendor/model` routes to
a connected provider:

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

The vendor names the model family, not its hosting platform: Claude on Vertex
is still `anthropic/…`. If unavailable, San uses the parent's provider and
model.

The foreground agent selects an agent by `description` / `when-to-use`; that
also selects its model.

## Permission Mode

Subagents have no UI, so permission prompts cannot be shown. The mode
controls what happens when a tool call would normally `ask`:

| Mode | Behavior |
|---|---|
| `explore` | Read-only; mutating tools are unavailable. |
| `default` | Reads allowed; `ask` becomes `deny`, unless allowed by `allow_tools`. |
| `edit` (= `acceptEdits`) | Edit/Write allowed; other gated tools, such as unrestricted Bash, are denied. |
| `bypassPermissions` | Allows everything after `deny_tools` and the bypass-immune floor (destructive/sensitive calls stay denied). Use only for trusted agents. |

Subagents cannot spawn subagents. `SendMessage(to="main", ...)` follows the
normal mode rules.

See [`packages/broker.md`](../packages/2-feature/broker.md)
for the message queue and [`concepts/permission-model.md`](../concepts/permission-model.md)
for the full decision pipeline.

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
3. Runs it foreground, or as a `task.AgentTask` when `run_in_background` is true.
4. Returns a tool result (foreground) or completion `<task-notification>`
   (background).

## Talking to a Running Worker

A background subagent registers with the broker while running:

- **Steer**: `SendMessage(to=<task id>, message)` sends a message that it reads
  on its next step. Delivery is best-effort.
- **Report to main**: `SendMessage(to="main", message)` sends an interim note.
  The final answer returns automatically on completion.
- **Stop**: `Agent` with `signal: "stop"` and the task id cancels the run. Start a new subagent to continue.

## Trying It

1. Save the agent file.
2. Restart `san`.
3. Ask the foreground model something that should trigger spawning:
   "use the test-runner agent to check whether tests pass".
4. Watch the task panel for a background subagent's progress; its final
   result arrives as a `<task-notification>` in the parent's conversation.
   A foreground subagent returns its final result directly as the `Agent`
   tool result.

## Common Pitfalls

- **No prompt argument.** The `Agent` call fails because `prompt` is
  required.
- **`allow_tools` too narrow.** Forgot `Bash`? The subagent can't run
  anything — and a whitelist without `SendMessage` also removes reporting
  to main.
- **`bypassPermissions` on a write-class subagent.** Verify carefully
  — the subagent has no human in the loop.
- **Same name in several locations.** Priority is project `.san` > user
  `~/.san` > project `.claude` > user `~/.claude` > plugins; the
  higher-priority definition wins.

## See Also

- [`packages/broker.md`](../packages/2-feature/broker.md) —
  how the main conversation and its subagents message each other.
- [`packages/subagent.md`](../packages/2-feature/subagent.md) — registry +
  executor design.
- [`packages/agent.md`](../packages/2-feature/agent.md) — foreground agent
  lifecycle.
- [`concepts/extension-model.md`](../concepts/extension-model.md).
- [`concepts/permission-model.md`](../concepts/permission-model.md).
