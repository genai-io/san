<div align="center">
  <h1>&lt; SAN ✦ /&gt;</h1>
  <p><strong>Minimal overhead. Maximum agent.</strong></p>
  <p>Lean context, native speed, open all the way down.</p>
  <p>
    <a href="https://github.com/genai-io/san/releases"><img src="https://img.shields.io/github/v/release/genai-io/san?style=flat-square" alt="Release"></a>
    <a href="https://genai-io.github.io/san/"><img src="https://img.shields.io/badge/Website-0d9488?style=flat-square" alt="Website"></a>
    <a href="https://genai-io.github.io/san/getting-started.html"><img src="https://img.shields.io/badge/Getting%20Started-0d9488?style=flat-square" alt="Getting Started"></a>
    <a href="docs/index.md"><img src="https://img.shields.io/badge/Docs-0d9488?style=flat-square" alt="Docs"></a>
    <a href="https://www.producthunt.com/products/san?launch=san"><img src="https://img.shields.io/badge/Product%20Hunt-da552f?style=flat-square&logo=producthunt&logoColor=white" alt="Product Hunt"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?style=flat-square" alt="License"></a>
  </p>
  <p>
    <strong>English</strong> · <a href="README.zh.md">简体中文</a>
  </p>
  <p>
    <a href="https://genai-io.github.io/san/intro.html?variant=readme"><picture>
      <source media="(prefers-color-scheme: dark)" srcset="assets/san-intro-dark.gif">
      <img src="assets/san-intro.gif" alt="San — animated intro" width="100%">
    </picture></a>
  </p>
  <sub><a href="https://genai-io.github.io/san/intro.html?variant=readme">Open the full-quality intro ↗</a></sub>
</div>

San is an open-source terminal agent runtime: one native Go binary, no Node.js
or Python. Everything the model touches — prompt, tools, providers, extensions —
stays yours to change.

## Why San

- **Small and fast** — Native execution, zero runtime dependencies, and minimal context overhead.
- **A less opinionated harness** — A model-agnostic runtime with a consistent interface for shaping agent behavior through system prompts, personas, skills, and tools.
- **Multi-agent coordination** — Subagent collaboration, cross-session messaging, and dynamic workflows.
- **Control and visibility** — Configure execution permissions and inspect model context and tool calls.

[Feature overview](docs/features.md) · [Architecture and diagram](docs/concepts/architecture.md)

<sub>*The name — **San**, written **三** ("three") and drawn **☰**. From the Dao De Jing, 三生万物 — "three begets the ten-thousand things": one runtime that becomes any agent, running a three-step loop (reason → act → observe). The command stays `san`.*</sub>

## Installation

**Homebrew (macOS / Linux)**

```bash
brew install genai-io/san/san
```

**macOS / Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/genai-io/san/main/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/genai-io/san/main/install.ps1 | iex
```

<details>
<summary><b>Other installation methods and uninstall</b></summary>

**Uninstall**

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/genai-io/san/main/install.sh | bash -s uninstall
```

```powershell
# Windows (PowerShell)
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/genai-io/san/main/install.ps1))) uninstall
```

**Go Install (requires Go 1.26.9+)**

```bash
go install github.com/genai-io/san/cmd/san@latest
```

**Build from Source**

```bash
git clone https://github.com/genai-io/san.git
cd san
go build -o san ./cmd/san
mkdir -p ~/.local/bin && mv san ~/.local/bin/
```

</details>

<details>
<summary><b>Updates</b></summary>

- **Automatic:** new releases install in the background; the status line prompts you to restart.
- **Manual:** run `san update`, or `brew update && brew upgrade genai-io/san/san` for Homebrew installations. The Homebrew formula syncs daily, so newly published releases may take time to appear.
- **Disable automatic updates:** use `/settings` › General or set `SAN_DISABLE_AUTOUPDATE=1`.

</details>

## Usage

Run `san`, then choose a model and add its API key when prompted.

```bash
san                          # interactive
san "explain this function"  # one-shot
san -p "do something"        # print mode (no TUI), pipe-friendly
san --continue               # resume the latest session
san --resume                 # pick a past session to resume
```

Use `san --help` for CLI options and `/help` for interactive commands.

## Benchmark: San vs Claude Code

Compared with [Claude Code](https://claude.ai/code) v2.1.112 on Apple Silicon, same model (`claude-sonnet-4-6`):

| Metric | San | Claude Code | Advantage |
|--------|---------|-------------|-----------|
| Download size | 12 MB | 63 MB (+ Node.js 112 MB) | **5x smaller** |
| Disk footprint | 38 MB | 175 MB | **4.6x smaller** |
| Startup time | ~0.01s | ~0.20s | **20x faster** |
| Startup memory | ~32 MB | ~189 MB | **5.8x less** |
| Simple task | ~2.4s / 39 MB | ~10.4s / 286 MB | **4.3x faster, 7.3x less memory** |
| Tool-use task | ~3.3s / 39 MB | ~26.0s / 285 MB | **7.9x faster, 7.2x less memory** |
| Harness context* | ~2.3k tokens | ~20.9k tokens | **~9x leaner** |

<sub>*Context overhead is system prompt + tool schemas on an empty first turn, measured separately on San v1.22.0 vs Claude Code v2.1.220 — see [method](docs/operations/benchmark.md#7-context-overhead-first-turn). All other rows are from the v1.13.2 / v2.1.112 run.</sub>

Comparable feature sets — the gap is client-side overhead, not capability. [`docs/operations/benchmark.md`](docs/operations/benchmark.md)

## Documentation

- [Getting started](docs/guides/getting-started.md) — usage and interactive controls
- [Configuration](docs/reference/configuration.md) — shell, models, credentials, and config files
- [Documentation Index](docs/index.md) — map of architecture, features, operations, and references
- [Architecture](docs/concepts/architecture.md) — architecture entrypoint and reading order
- [Package Map](docs/reference/package-map.md) — package ownership and dependency boundaries
- [Personas](docs/concepts/persona.md) — bundled system prompt, skills, agents, and settings
- [System Prompt](docs/concepts/harness-channels.md) — Slot model, persona, skill/agent injection
- [Subagents](docs/packages/2-feature/subagent.md) · [Skills](docs/packages/2-feature/skill.md) · [Plugins](docs/packages/2-feature/plugin.md) · [MCP](docs/packages/2-feature/mcp.md)
- [Hooks](docs/packages/2-feature/hook.md) · [Permissions](docs/concepts/permission-model.md) · [Tasks](docs/packages/2-feature/task.md)
- [Workflows](docs/design/proposals/0001-workflow-orchestration.md) · [Session groups](docs/design/proposals/0003-session-groups.md) — subagents as a graph; sessions that message each other
- [Inspector](docs/packages/2-feature/inspector.md) — local web UI for transcript replay and debugging
- Per-package design under [`docs/packages/`](docs/packages/) — start at [Package Index](docs/packages/index.md)

## Related Projects

- [Claude Code](https://claude.ai/code) — Anthropic's AI coding assistant
- [Aider](https://github.com/Aider-AI/aider) — AI pair programming in terminal
- [Continue](https://github.com/continuedev/continue) — Open-source AI code assistant

## Community

Two ways in — WeChat for the Chinese community, Slack for everyone else:

- **WeChat** — 关注公众号「极客外传」· 回复 `san` 或 `三` 入群
- **Slack** — [Join our Slack](https://join.slack.com/t/sanaico/shared_invite/zt-3zvfr8v6f-dchFpvpufY7fKA7tG7lhIg)

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.
