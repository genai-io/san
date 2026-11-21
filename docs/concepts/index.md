# Concepts

Cross-cutting explanations — how the system works across multiple packages.
For per-package internals see [`../packages/index.md`](../packages/index.md);
for task how-tos see [`../guides/index.md`](../guides/index.md).

| Page | What it explains |
|---|---|
| [`architecture`](architecture.md) | System overview: the five layers, runtime model, core primitives. |
| [`data-flow`](data-flow.md) | The full input → agent → render path. (中文: `data-flow.zh.md`) |
| [`rendering`](rendering.md) | How model output becomes terminal frames. (中文: `rendering.zh.md`) |
| [`extension-model`](extension-model.md) | Skills, plugins, MCP, hooks, commands, subagents — how San is extended. |
| [`auto-memory`](auto-memory.md) | The agent's own memory: stores, recall, saving, what the screen shows. |
| [`permission-model`](permission-model.md) | How tool-call permissions are decided and gated. |
| [`persona`](persona.md) | Switchable system prompt + skills + config bundles. |
| [`flow`](flow.md) | Subagent collaboration blueprints, scheduling, task-area graphs and recovery. |
