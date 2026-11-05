# Packages

Implementation notes, listed by package. See the [package map](../reference/package-map.md)
and [dependency rules](../reference/dependency-rules.md) for ownership and layers.

| Package | Purpose |
|---|---|
| [agent](agent.md) | Main agent lifecycle |
| [app](app.md) | Terminal UI and event routing |
| [broker](broker.md) | Agent messaging |
| [cmd](cmd.md) | CLI entrypoint and wiring |
| [command](command.md) | Slash commands |
| [core](core.md) | Shared agent contracts |
| [cron](cron.md) | Scheduled and recurring tasks |
| [hook](hook.md) | Lifecycle hooks |
| [infrastructure](infrastructure.md) | Logging, secrets, files, and process helpers |
| [inspector](inspector.md) | Transcript inspection and replay |
| [llm](llm.md) | Models and providers |
| [mcp](mcp.md) | External tool servers |
| [plugin](plugin.md) | Extension installation and loading |
| [reminder](reminder.md) | Context reminders |
| [search](search.md) | Web search backends |
| [selflearn](selflearn.md) | Background skill review |
| [session](session.md) | Session persistence, resume, and fork |
| [setting](setting.md) | Configuration and permission decisions |
| [skill](skill.md) | Skill loading and visibility |
| [subagent](subagent.md) | Subagent definitions and execution |
| [task](task.md) | Background tasks and progress |
| [tool](tool.md) | Tool schemas and execution |
| [transcript](transcript.md) | Event logs and recovery |
| [workflow](workflow.md) | Multi-agent workflow scheduling |

Use package names for filenames and `.zh.md` for translations.
New package pages follow the [template](TEMPLATE.md).
