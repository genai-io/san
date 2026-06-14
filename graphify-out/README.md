# Graphify — SAN Project Knowledge Graph / SAN 项目知识图谱

English | [中文版](#中文版)

## Why Graphify? / 为什么用 Graphify？

SAN is a terminal-native project — developed with AI coding assistants (Claude Code, SAN itself), not IDEs. There are no `.vscode/` or `.idea/` project files because the development workflow is **vibe coding in the terminal**. In this paradigm, code navigation is done through grep and Read, not click-to-definition — every lookup burns context window tokens.

SAN 是一个终端原生的项目 — 使用 AI 编程助手（Claude Code、SAN 自身）开发，不依赖 IDE。项目中没有 `.vscode/` 或 `.idea/` 文件，因为开发工作流就是**终端中的 vibe coding**。在这种范式下，代码导航靠 grep 和 Read，而非点击跳转 — 每次查找都消耗上下文窗口 token。

Graphify is the **navigation layer** for terminal-first development: a pre-built knowledge graph that answers structural questions in one query instead of multiple grep/Read cycles.

Graphify 是终端优先开发的**导航层**：预构建的知识图谱，用一次查询替代多轮 grep/Read 循环。

```bash
# Instead of: grep → Read → grep → Read → grep → Read
/graphify affected "ProviderSelector"    # Who depends on it? / 谁依赖它？
/graphify path "NewAgent" "Session"      # Dependency chain / 依赖链
/graphify explain "HookEngine"           # What's nearby? / 邻接关系
```

## What's in graphify-out/ / 输出文件

| File / 文件 | Size / 大小 | Purpose / 用途 |
|---|---|---|
| `graph.html` | 5.8 MB | Interactive visualization — open in browser / 交互式可视化，浏览器打开 |
| `graph.json` | 6.5 MB | Graph data for query/path/affected commands / 查询命令的数据源 |
| `GRAPH_REPORT.md` | 103 KB | Analysis — god nodes, communities, surprising connections / 分析报告 |
| `USAGE.md` | 4 KB | Bilingual command reference + workflows / 中英双语命令参考 |
| `manifest.json` | 89 KB | File manifest for incremental updates / 增量更新清单 |

## Setup / 安装

```bash
# 1. Install Graphify (requires Python 3.10+)
#    安装 Graphify（需要 Python 3.10+）
uv tool install graphifyy
# or: pipx install graphifyy

# 2. Register skill with Claude Code
#    注册到 Claude Code
graphify install

# 3. Done — the pre-built graph is already in this directory
#    完成 — 预构建的图谱已在此目录中
```

If you need semantic extraction (docs/images), also install the Anthropic backend:
如需语义提取（文档/图片），还需安装 Anthropic 后端：
```bash
uv tool install "graphifyy[anthropic]" --force
```

## Quick Start / 快速开始

```bash
# Query the graph / 查询图谱
/graphify query "how does the permission system work"

# Open interactive visualization / 打开交互式可视化
xdg-open graphify-out/graph.html
```

See [`USAGE.md`](USAGE.md) for full command reference and workflows (feature development, bug fixing, refactoring).

完整命令参考和工作流（开发新功能、修复 Bug、重构）请参见 [`USAGE.md`](USAGE.md)。

## Graph Stats / 图谱统计

- **7123 nodes** / 节点 · **13775 edges** / 边 · **487 communities** / 社区
- **85% EXTRACTED** (from AST) · **15% INFERRED** · **0% AMBIGUOUS**
- No import cycles detected / 无导入循环

### Top God Nodes / 核心节点

1. `contains()` — 211 edges (cross-community hub / 跨社区枢纽)
2. `PluginSelector` — 72 edges
3. `ProviderSelector` — 60 edges
4. `NewEngine()` — 58 edges
5. `NewData()` — 55 edges

## Maintenance / 维护

```bash
# After code changes — incremental, free (AST only) / 增量更新，免费
graphify update .

# Re-cluster and label new communities (uses LLM) / 重新聚类和命名（使用 LLM）
graphify cluster-only .
graphify label . --backend=claude
```

## Relationship to existing docs / 与现有文档的关系

Graphify and SAN's documentation serve different purposes in a terminal-first workflow:

Graphify 和 SAN 的文档在终端优先的工作流中各司其职：

| Need / 需求 | Use / 使用 |
|---|---|
| Package design and contracts / 包设计和契约 | `docs/packages/*.md` |
| Layer boundaries and ownership / 层级边界 | `docs/reference/package-map.md` |
| Cross-module dependency tracing / 跨模块依赖追踪 | **Graphify** `path` / `affected` |
| Impact analysis before changes / 变更前的冲击分析 | **Graphify** `affected` / `explain` |
| Code navigation in terminal / 终端中的代码导航 | **Graphify** `query` |
