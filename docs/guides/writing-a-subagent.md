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
mode: bypassPermissions
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
| `name` | yes | Subagent type identifier; used in `Agent` tool's `agent_type` field. |
| `description` | yes | Shown in selectors and used by the foreground model to decide when to spawn this agent. |
| `allow_tools` | no | Restrict the subagent's tool set. Default = same as parent. |
| `mode` | no | One of `default`, `explore`, `acceptEdits`, `bypassPermissions`, `dontAsk`, or `auto`. Default = `default`. |
| `model` | no | Pin a model for this subagent; otherwise inherits the parent's. Claude aliases (`opus`/`sonnet`/`haiku`) apply only on an Anthropic parent; on other providers they inherit the parent model. A bare id uses the parent's provider; `vendor/model` routes to another **connected** provider. |

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
- an alias (`opus` / `sonnet` / `haiku`) — resolved on an Anthropic parent;
  otherwise it inherits the parent model. Use `anthropic/model` to route
  explicitly from another provider.
- a bare id — served by the parent's provider.
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
| `default` | `ask` collapses to `deny` (with a "would have asked" record). |
| `acceptEdits` | Treat `ask` as `allow` for edit-class tools. |
| `bypassPermissions` | Treat every `ask` as `allow`. Use only for read-only or trusted subagents. |
| `plan` | Force `deny` for all write-class tools. Read-only exploration. |

See [`concepts/permission-model.md`](../concepts/permission-model.md) for
the full decision pipeline.

## Workspace Coordination

Subagents share the parent conversation's working directory. Use read-only
permission modes for parallel reviewers, wait for all reviewers to finish,
then let one agent apply the aggregated changes. Do not run multiple writing
subagents concurrently against the same files.

## How the Parent Spawns It

The foreground agent calls the `Agent` tool with:

```json
{
  "agent_type": "test-runner",
  "description": "Run the unit tests for the auth package",
  "prompt": "Focus on internal/auth/*_test.go and report failures."
}
```

San:

1. Looks up `test-runner` in the subagent registry.
2. Builds a `core.Agent` with the subagent's system prompt and tool subset.
3. Runs it as a `task.AgentTask` in the background.
4. Returns the final aggregated result to the parent agent.

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
- **`allowed-tools` too narrow.** Forgot `Bash`? The subagent can't
  run anything.
- **`bypassPermissions` on a write-class subagent.** Verify carefully
  — the subagent has no human in the loop.

## See Also

- [`packages/subagent.md`](../packages/2-feature/subagent.md) — registry +
  executor design.
- [`packages/agent.md`](../packages/2-feature/agent.md) — foreground agent
  lifecycle.
- [`concepts/extension-model.md`](../concepts/extension-model.md).
- [`concepts/permission-model.md`](../concepts/permission-model.md).
