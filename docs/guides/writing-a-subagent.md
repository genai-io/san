# Writing a Subagent

A subagent is a markdown-defined **agent type** — a system prompt + tool
subset + permission mode that the foreground agent can spawn via the
`Agent` tool. Subagents run in parallel and report results back through
the tool result.

For the system-level design see [`packages/subagent.md`](../packages/subagent.md)
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
allowed-tools: [Bash, Read, Grep]
permission-mode: bypass
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
| `name` | yes | Subagent type identifier; used in `Agent` tool's `agent_type` field. |
| `description` | yes | Shown in selectors and used by the foreground model to decide when to spawn this agent. |
| `allowed-tools` | no | Restrict the subagent's tool set. Default = same as parent. |
| `permission-mode` | no | One of `default`, `acceptEdits`, `bypassPermissions`, `plan`. Default = parent. |
| `isolation` | no | `none` (default) or `worktree` — see below. |
| `model` | no | Pin a specific model for this subagent; otherwise inherits parent's model. |

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

## Worktree Isolation

`isolation: worktree` creates a `git worktree` under
`<project>/.git/agent-worktrees/<random>/` and runs the subagent there.
The parent's working tree is untouched until the subagent finishes; the
worktree is removed on success.

Use this when you want a subagent to run experiments that may dirty the
tree (build artifacts, codegen, refactors) without polluting the
foreground session.

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
- **`isolation: worktree` without git.** Fails silently for non-git
  projects. The subagent runs in the parent's cwd as fallback.

## See Also

- [`packages/subagent.md`](../packages/subagent.md) — registry +
  executor design.
- [`packages/agent.md`](../packages/agent.md) — foreground agent
  lifecycle.
- [`concepts/extension-model.md`](../concepts/extension-model.md).
- [`concepts/permission-model.md`](../concepts/permission-model.md).
