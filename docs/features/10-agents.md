# Feature 10: Agents / Subagent System

## Overview

Agents are defined in AGENT.md files. They run their own `core.Agent` with an independent system prompt, tool set, and permission mode. They can be invoked headlessly or spawned from within the TUI via the `Agent` tool. See [System Prompt](../system-prompt.md) for how the subagent's identity is built from `AgentConfig`.

For the `explore` permission mode contract, see [Feature 22](./22-explore-agent.md).

**AGENT.md frontmatter:**

```yaml
---
name: CodeReviewer
description: Reviews code changes for quality issues
model: inherit          # inherit | sonnet | opus | haiku
permission-mode: default
tools:
  - Read
  - Glob
  - Grep
skills: []
system-prompt: ""
max-turns: 100
mcp-servers: []
---
```

**Permission modes:**

| Mode | Behavior |
|------|----------|
| `explore` | Non-mutating investigation only: Read, Glob, Grep, WebFetch, WebSearch |
| `edit` | File edits allowed; tools requiring approval are unavailable to subagents |
| `default` | Default subagent access |

**Headless execution:**

```bash
gen agent run --type AgentName --prompt "task"
```

**Invocation options:** `model` override and `max-turns` override for headless runs; the in-TUI `Agent` tool also supports launching a single background subagent with `run_in_background=true`

## UI Interactions

- **`/agents`**: picker to enable/disable agents.
- **Agent tool call**: TUI shows `SubagentStart` notification; progress indicator runs while the agent is active.
- **Agent output**: streamed back to the parent conversation as a tool result.
- **Background agents**: tracked in the task panel (Alt+T).
- **Single background subagent**: one agent can be launched independently, return a task ID immediately, and continue running while the main thread handles new prompts.

## Automated Tests

```bash
go test ./internal/agent/... -v
go test ./internal/subagent/... -v
go test ./tests/integration/agent/... -v
```

Covered:

```
# Internal tests
TestAgentLazyLoading                        — agents loaded on demand

# Integration tests
TestAgent_GeneralExploreMode                — general-purpose agent in explore mode
TestAgent_UnknownAgent                      — unknown agent returns error
TestAgent_MaxTurnsRespected                 — max-turns limit enforced
TestAgent_ModelResolution                   — model inheritance and override
TestAgent_ExploreMode_BlocksWrites          — explore mode blocks writes in agent
TestAgent_SubagentHooks_Fire                — SubagentStart and SubagentStop hooks fire
TestAgent_BackgroundExecution               — background agent tracked in task system
TestExecuteSubmitRequest_CancelsPendingToolsBeforeNewTurn — a new user turn closes pending tool_use blocks before continuing

# Executor tests
TestPrepareRunConfigRespectsOverrides              — run config overrides work
TestPrepareRunConfigUsesResolvedExploreModePrompt        — explore mode prompt resolved
TestBuildCancelledAgentResultUsesPreparedRunMetadata — cancelled agent metadata
```

Cases to add:

```go
func TestAgent_ProgressCallback_Fires(t *testing.T) {
    // Progress callback must fire for each turn
}

func TestAgent_ToolRestriction_Enforced(t *testing.T) {
    // Agent with tools: [Read, Glob] must not have access to Write or Bash
}

func TestAgent_ModelOverride_Request(t *testing.T) {
    // Agent with model: sonnet must use sonnet for all LLM calls
}

func TestAgent_IndependentSystemPrompt(t *testing.T) {
    // Agent must use its own AGENT.md content as system prompt
}

func TestAgent_ExploreMode_FiltersToolSchemas(t *testing.T) {
    // Agent with explore mode must only expose non-mutating tools
}

func TestAgent_EditMode_BlocksApprovalOnlyTools(t *testing.T) {
    // Agent with edit mode can write files but cannot run approval-only tools
}

```

## Interactive Tests (tmux)

```bash
mkdir -p /tmp/agent_test/.gen/agents
echo "hello from agent test" > /tmp/agent_test/sample.txt

cat > /tmp/agent_test/.gen/agents/FileReader.md << 'EOF'
---
name: FileReader
description: Reads and summarizes text files
model: inherit
permission-mode: default
tools:
  - Read
  - Glob
max-turns: 10
---

You are a file reading agent. Read the requested file and provide a concise summary.
EOF

tmux new-session -d -s t_agent -x 220 -y 60

# Test 1: Headless execution
tmux send-keys -t t_agent 'cd /tmp/agent_test && gen agent run --type FileReader --prompt "read sample.txt and summarize"' Enter
sleep 15
tmux capture-pane -t t_agent -p
# Expected: agent reads file, outputs summary, exits cleanly

# Test 2: Agent invoked from TUI
tmux send-keys -t t_agent 'cd /tmp/agent_test && gen' Enter
sleep 2
tmux send-keys -t t_agent 'use the FileReader agent to read sample.txt' Enter
sleep 15
tmux capture-pane -t t_agent -p
# Expected: SubagentStart shown; agent output in conversation; SubagentStop fires

# Test 3: /agents picker
tmux send-keys -t t_agent '/agents' Enter
sleep 1
tmux capture-pane -t t_agent -p
# Expected: agent selector titled "Manage Agents" with "FileReader" listed

# Test 4: Start a single background subagent
tmux send-keys -t t_agent Escape
tmux send-keys -t t_agent 'run the FileReader agent in background to read sample.txt' Enter
sleep 5
tmux capture-pane -t t_agent -p
# Expected: tool result shows "background (Task ID: ...)" and the main thread is idle again

# Test 5: Background agent tracked in task panel
tmux send-keys -t t_agent C-t
sleep 1
tmux capture-pane -t t_agent -p
# Expected: task panel shows agent task with "Running" or "Completed" status

# Test 6: Check background status without blocking the main thread
tmux send-keys -t t_agent C-t
tmux send-keys -t t_agent 'check the background subagent status' Enter
sleep 5
tmux capture-pane -t t_agent -p
# Expected: TaskOutput reports current status immediately; it should not wait by default

# Test 7: Explicitly wait for completion only when needed
tmux send-keys -t t_agent 'wait for the background subagent result now' Enter
sleep 10
tmux capture-pane -t t_agent -p
# Expected: TaskOutput with block=true may wait; final output is shown when the task completes

# Test 8: Agent with explore permission mode
cat > /tmp/agent_test/.gen/agents/ReadOnly.md << 'EOF'
---
name: Explorer
description: Explore-only agent
model: inherit
permission-mode: explore
tools:
  - Read
  - Glob
  - Write
max-turns: 5
---

You are an explore-mode agent. Try to read and write files.
EOF
tmux send-keys -t t_agent C-t
tmux send-keys -t t_agent 'use the Explorer agent to create a file test.txt' Enter
sleep 10
tmux capture-pane -t t_agent -p
# Expected: agent cannot write — Write tool blocked by explore mode

tmux kill-session -t t_agent
rm -rf /tmp/agent_test
```
