# San vs Claude Code: Performance Benchmark

Comparison between **San v1.13.2** (Go) and **Claude Code v2.1.112** (Node.js/TypeScript).

**Environment**: macOS Darwin 25.4.0, Apple Silicon (arm64)
**Model**: Both use `claude-sonnet-4-6` via Anthropic API
**Date**: 2026-04-21

---

## 1. Distribution Size

| Metric | San | Claude Code | Ratio |
|--------|---------|-------------|-------|
| Download size | **12 MB** (.tar.gz) | 63 MB (npm) | **5x smaller** |
| Binary / Package (on disk) | 38 MB | 63 MB | 0.6x |
| Runtime dependency | None (static binary) | Node.js v24 (~112 MB) | - |
| Total disk footprint | **38 MB** | **~175 MB** (63 + 112) | **4.6x smaller** |
| File count | 1 | ~30 + node_modules | - |

San ships as a single static binary with zero runtime dependencies. Claude Code requires Node.js and installs ~63 MB of npm packages.

For what this small, dependency-free footprint enables — Raspberry Pi, edge nodes, minimal containers, air-gapped hosts — see [footprint.md](footprint.md).

---

## 2. Startup Time (`version`)

| Run | San | Claude Code |
|-----|---------|-------------|
| 1 | 0.01s | 0.20s |
| 2 | 0.01s | 0.19s |
| 3 | 0.01s | 0.20s |
| 4 | 0.01s | 0.19s |
| 5 | 0.01s | 0.20s |
| **Avg** | **~0.01s** | **~0.20s** |

San starts **~20x faster**.

---

## 3. Startup Memory (`version`)

| Run | San | Claude Code |
|-----|---------|-------------|
| 1 | 32.3 MB | 188.8 MB |
| 2 | 32.6 MB | 188.6 MB |
| 3 | 32.2 MB | 188.5 MB |
| 4 | 32.7 MB | 188.4 MB |
| 5 | 32.0 MB | 188.5 MB |
| **Avg** | **~32 MB** | **~189 MB** |

San uses **~5.8x less memory** at startup. The Node.js runtime alone accounts for a large portion of Claude Code's baseline.

---

## 4. Simple Task: "What is 2+2?"

Non-interactive print mode (`-p`), measuring total wall time and peak RSS.

| Run | San (time / RSS) | Claude Code (time / RSS) |
|-----|----------------------|--------------------------|
| 1 | 2.09s / 39.3 MB | 9.90s / 277.9 MB |
| 2 | 2.04s / 38.6 MB | 10.00s / 290.9 MB |
| 3 | 2.13s / 39.0 MB | 10.56s / 292.2 MB |
| 4 | 3.88s / 39.0 MB | 10.28s / 280.2 MB |
| 5 | 2.07s / 39.1 MB | 11.18s / 286.6 MB |
| **Avg** | **2.44s / 39.0 MB** | **10.38s / 285.5 MB** |

- Response time: San **~4.3x faster**
- Memory: San **~7.3x less**

Note: Both tools use the same Anthropic API and model. The time difference reflects client-side overhead (startup, system prompt construction, session management, hooks, etc.), not LLM inference time.

---

## 5. File Read Task: "Read main.go and count lines"

Requires tool use (Read tool) + LLM response.

| Run | San (time / RSS) | Claude Code (time / RSS) |
|-----|----------------------|--------------------------|
| 1 | 3.27s / 38.8 MB | 18.99s / 279.0 MB |
| 2 | 2.77s / 39.2 MB | 17.84s / 288.8 MB |
| 3 | 2.82s / 38.6 MB | 18.18s / 278.0 MB |
| **Avg** | **2.95s / 38.9 MB** | **18.34s / 281.9 MB** |

- Response time: San **~6.2x faster**
- Memory: San **~7.3x less**

---

## 6. Tool-Use Task: "Count .go files in internal/tool"

Requires Glob/Bash tool call + counting + response.

| Run | San (time / RSS) | Claude Code (time / RSS) |
|-----|----------------------|--------------------------|
| 1 | 4.44s / 39.5 MB | 24.05s / 286.5 MB |
| 2 | 2.72s / 39.4 MB | 25.23s / 275.7 MB |
| 3 | 2.98s / 39.5 MB | 23.61s / 287.7 MB |
| 4 | 3.38s / 38.6 MB | 32.32s / 287.9 MB |
| 5 | 2.93s / 39.6 MB | 24.57s / 286.6 MB |
| **Avg** | **3.29s / 39.3 MB** | **25.96s / 284.9 MB** |

- Response time: San **~7.9x faster**
- Memory: San **~7.2x less**

---

## Summary

| Metric | San | Claude Code | San Advantage |
|--------|---------|-------------|-------------------|
| Download size | 12 MB | 63 MB (+ Node.js) | **5x smaller** |
| Disk footprint | 38 MB | 175 MB | **4.6x smaller** |
| Startup time | ~0.01s | ~0.20s | **20x faster** |
| Startup memory | ~32 MB | ~189 MB | **5.8x less** |
| Simple task time | ~2.4s | ~10.4s | **4.3x faster** |
| Simple task memory | ~39 MB | ~286 MB | **7.3x less** |
| File read task time | ~3.0s | ~18.3s | **6.2x faster** |
| File read task memory | ~39 MB | ~282 MB | **7.3x less** |
| Tool-use task time | ~3.3s | ~26.0s | **7.9x faster** |
| Tool-use task memory | ~39 MB | ~285 MB | **7.2x less** |

### Why the difference?

Both tools have comparable feature sets (hooks, skills, plugins, session management, MCP, subagents, etc.). The performance gap comes from the underlying technology:

- **Language runtime**: Go compiles to native code with a lightweight runtime (~32 MB baseline). Node.js has a heavier runtime with JIT compilation, garbage collector, and V8 engine overhead (~189 MB baseline).
- **Architecture**: San is a single static binary with zero dependencies. Claude Code is a bundled TypeScript application running on Node.js with npm dependencies.
- **Feature differences**: Claude Code has some additional features (IDE integration, OAuth, Chrome integration, Teams, prompt caching) that add incremental overhead.

### Caveats

- LLM inference time is identical (same API, same model) — the difference is purely client-side overhead.
- Network latency variance affects individual runs; averages across 3-5 runs are more reliable.
- Memory is measured as peak RSS; actual working set may differ.
