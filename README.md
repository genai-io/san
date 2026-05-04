<div align="center">
  <h1>&lt; GEN ✦ /&gt;</h1>
  <p><strong>Open-source AI coding assistant for the terminal</strong></p>
  <p>
    <a href="https://github.com/genai-io/gen-code/releases"><img src="https://img.shields.io/github/v/release/genai-io/gen-code?style=flat-square" alt="Release"></a>
    <a href="https://goreportcard.com/report/github.com/genai-io/gen-code"><img src="https://goreportcard.com/badge/github.com/genai-io/gen-code?style=flat-square" alt="Go Report Card"></a>
    <a href="https://pkg.go.dev/github.com/genai-io/gen-code"><img src="https://pkg.go.dev/badge/github.com/genai-io/gen-code.svg" alt="Go Reference"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?style=flat-square" alt="License"></a>
  </p>
</div>

Gen Code is a terminal coding assistant with interchangeable LLM providers, assistant personas, and search engines. Existing [Claude Code](https://claude.ai/code) skills, plugins, and project instructions work unchanged. Implemented in Go as a single binary with concurrent multi-agent orchestration.

## Features

### Open architecture

- **LLM providers** — Anthropic, OpenAI, Google, Moonshot, Alibaba, MiniMax. Runtime selection via `/model`.
- **Assistant personas** — Markdown-defined identities at user or project scope; switch with `/identity` ([details](docs/system-prompt.md#identity-custom-personas)).
- **Search engines** — Exa, Brave, or Serper; switch with `/search`.

### Extensibility

- **Claude Code compatible** — Existing `~/.claude/`, `.claude/`, plugin marketplaces, and MCP server definitions integrate without modification.
- **Skills** — Three-state lifecycle (active / enabled / disabled), per user or per project, managed via `/skills`.
- **Subagents** — Per-agent permission modes (`explore` / `edit` / `default`), tool whitelists, and optional git-worktree sandboxing. Managed via `/agents` and the `Agent` tool.
- **Lifecycle hooks** — Shell, LLM, agent, and HTTP backends across the full Claude Code event set.
- **Project instructions** — `GEN.md` or `CLAUDE.md` auto-loaded into the system prompt.

### Engineering

- **Native performance** — Single Go binary; see [benchmark](#benchmark-gencode-vs-claude-code) for measured numbers.
- **Event-driven coordination** — Parallel subagent execution via a pub/sub hub ([architecture](docs/subagent.md)).
- **Session persistence** — Auto-save, resume, fork, and automatic context compaction.
- **Prompt prediction** — Speculative completion of likely next prompts to reduce latency.


## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/genai-io/gen-code/main/install.sh | bash
```

Re-run to upgrade. To uninstall:

```bash
curl -fsSL https://raw.githubusercontent.com/genai-io/gen-code/main/install.sh | bash -s uninstall
```

<details>
<summary><b>Other methods</b></summary>

**Go Install**

```bash
go install github.com/genai-io/gen-code/cmd/gen@latest
```

**Build from Source**

```bash
git clone https://github.com/genai-io/gen-code.git
cd gen-code
go build -o gen ./cmd/gen
mkdir -p ~/.local/bin && mv gen ~/.local/bin/
```

</details>

## Usage

```bash
# Interactive mode
gen

# Non-interactive mode
gen "explain this function"
cat main.go | gen "review this code"

# Resume previous session
gen --continue        # Resume most recent
gen --resume          # Select from list
```

### Quick Start

1. Run `gen` to start interactive mode
2. Use `/model` to connect a provider and select a model
3. Start chatting!

### Commands

`/model` `/tools` `/skills` `/agents` `/identity` `/mcp` `/plugin` `/compact` `/think` `/search` `/loop` `/resume` `/fork` `/clear` `/init` `/memory` — type `/help` for details.

Keyboard: `Shift+Tab` toggle permission mode, `Ctrl+O` expand tool details, `Ctrl+C` cancel, `Ctrl+D` exit.

## Configuration

User-level config lives in `~/.gen/`; project-level files in `<project>/.gen/` override per workspace. Place a `GEN.md` (or `CLAUDE.md`) in your project root for project-specific instructions — Gen Code auto-loads them into the system prompt.

### LLM provider credentials

| Provider | Environment Variables |
|:---------|:----------------------|
| **Anthropic** (Claude) | `ANTHROPIC_API_KEY` or [Vertex AI](https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/claude) |
| **OpenAI** (GPT, o-series, Codex) | `OPENAI_API_KEY` |
| **Google** (Gemini) | `GOOGLE_API_KEY` |
| **Moonshot** (Kimi) | `MOONSHOT_API_KEY` |
| **Alibaba** (Qwen, DeepSeek) | `DASHSCOPE_API_KEY` |
| **MiniMax** | `MINIMAX_API_KEY` |

Run `/model` in the TUI to see the currently available model list.

### Web-search backend credentials

| Backend | Environment Variable |
|:--------|:---------------------|
| **Exa** | `EXA_API_KEY` |
| **Brave Search** | `BRAVE_API_KEY` |
| **Serper** | `SERPER_API_KEY` |

<details>
<summary><b>Directory layout</b></summary>

User-level (`~/.gen/`):

```
providers.json    # Provider connections and current model
settings.json     # Permissions, hooks, env, identity
skills.json       # Skill states
identities/       # Custom personas (see /identity)
skills/           # Custom skill definitions
agents/           # Custom agent definitions
commands/         # Custom slash commands
plugins/          # Installed plugins
projects/         # Session transcripts + indexes
```

Project-level (`.gen/`):

```
settings.json      # Permissions, hooks, disabled tools
mcp.json           # MCP server definitions
identities/*.md    # Project-scoped personas (override user-level)
agents/*.md        # Subagent definitions
skills/*/SKILL.md  # Skills
commands/*.md      # Slash commands
```

</details>

## Benchmark: GenCode vs Claude Code

Compared with [Claude Code](https://claude.ai/code) v2.1.112 on Apple Silicon, same model (`claude-sonnet-4-6`):

| Metric | Gen Code | Claude Code | Advantage |
|--------|---------|-------------|-----------|
| Download size | 12 MB | 63 MB (+ Node.js 112 MB) | **5x smaller** |
| Disk footprint | 38 MB | 175 MB | **4.6x smaller** |
| Startup time | ~0.01s | ~0.20s | **20x faster** |
| Startup memory | ~32 MB | ~189 MB | **5.8x less** |
| Simple task | ~2.4s / 39 MB | ~10.4s / 286 MB | **4.3x faster, 7.3x less memory** |
| Tool-use task | ~3.3s / 39 MB | ~26.0s / 285 MB | **7.9x faster, 7.2x less memory** |

Both tools have comparable features (hooks, skills, plugins, session, MCP, etc.). The performance gap comes from Go's native compilation, minimal architecture design, and lean prompt engineering — vs Node.js V8/JIT/GC runtime overhead.

See full details: [docs/benchmark-gencode-vs-claudecode.md](docs/benchmark-gencode-vs-claudecode.md)

## Documentation

- [Architecture](docs/architecture.md) — TUI MVU model and package layout
- [System Prompt](docs/system-prompt.md) — Slot model, identity, skill/agent injection
- [Subagents](docs/subagent.md) · [Skills](docs/skill-system.md) · [Plugins](docs/plugin-system.md) · [MCP](docs/mcp-servers.md)
- [Hooks](docs/hook.md) · [Permissions](docs/gen-permission.md) · [Tasks](docs/task-management.md)
- Per-feature notes under [`docs/features/`](docs/features/)

## Related Projects

- [Claude Code](https://claude.ai/code) — Anthropic's AI coding assistant
- [Aider](https://github.com/paul-gauthier/aider) — AI pair programming in terminal
- [Continue](https://github.com/continuedev/continue) — Open-source AI code assistant

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.