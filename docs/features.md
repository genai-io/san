# Feature Overview

San's main modules and their capabilities. Follow each feature link for details.

## Memory and learning

- [Auto memory](concepts/auto-memory.md): user/project stores, automatic recall, updates, and forgetting.
- [Project instructions](concepts/harness-channels.md): layered `AGENTS.md` instructions and system-prompt context.
- [Self-learning](packages/2-feature/selflearn.md): background review that creates and refines reusable skills.

## Agent and session collaboration

- [Subagents](packages/2-feature/subagent.md): custom agents, isolated conversations, parallel delegation, and worker continuation.
- [Agent messaging](packages/2-feature/broker.md): instructions, feedback, and completion notices between main and subagents.
- [Session groups](design/proposals/0003-session-groups.md): same-machine cross-session messaging, membership, presence, offline queues, and hold controls.
- [Workflows](packages/2-feature/workflow.md): dependency-based execution of subagent turns and result summaries.

## Sessions, tasks, and automation

- [Sessions](packages/2-feature/session.md): persistent history and state, resume, and conversation forks.
- [Tasks and processes](packages/2-feature/task.md): foreground/background execution, shell jobs and services, progress, logs, completion notices, and stop controls.
- [Autopilot](guides/autopilot.md): goal-driven autonomous execution.
- [Scheduling](packages/2-feature/cron.md): one-shot prompts and recurring work.

## Tools and extensions

- [Built-in tools](packages/2-feature/tool.md): file operations, shell execution, and tool configuration.
- [Web search](packages/2-feature/search.md): pluggable search backends.
- [Skills](packages/2-feature/skill.md): reusable task instructions and procedures.
- [Plugins](packages/2-feature/plugin.md): installation and loading of extension bundles.
- [MCP](packages/2-feature/mcp.md): external tool-server connections.
- [Hooks](packages/2-feature/hook.md): lifecycle checks and automation.
- [Commands](packages/2-feature/command.md): built-in and custom slash commands.

## Models, behavior, and permissions

- [Models and providers](packages/2-feature/llm.md): provider connections, model selection, and inference settings.
- [Personas](concepts/persona.md): switchable bundles of prompts, skills, agents, and settings.
- [Settings](packages/2-feature/setting.md): user/project configuration and overrides.
- [Permissions](concepts/permission-model.md): approval policies and execution authority for main and subagents.

## Interaction and inspection

- [CLI](guides/getting-started.md): interactive terminal use, one-shot prompts, and headless/pipe-friendly execution.
- [Inspector](packages/2-feature/inspector.md): conversation replay, model-context inspection, and live event viewing.

Session groups connect independent sessions; agent messaging connects main and
its workers. Message acceptance means queued delivery, not acknowledgment or
completed work.
