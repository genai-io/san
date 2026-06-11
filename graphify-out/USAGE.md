# Graphify Usage Guide — SAN Project

English | [中文](#中文版)

> Knowledge graph for the SAN codebase: 7123 nodes, 13775 edges, 487 communities.
> Built from commit `319aa93a`.
>
> SAN 代码库知识图谱：7123 个节点、13775 条边、487 个社区。
> 基于 commit `319aa93a` 构建。

---

## Quick Start / 快速开始

```
/graphify query "your question / 你的问题"
```

---

## Commands / 命令参考

| Command / 命令 | Description / 说明 |
|---------|-------------|
| `/graphify .` | Rebuild/update graph / 重建或更新图谱 |
| `/graphify query "q"` | Natural language BFS query / 自然语言 BFS 遍历查询 |
| `/graphify path "A" "B"` | Shortest path between two concepts / 两个概念间的最短路径 |
| `/graphify explain "X"` | Explain a node and its neighbors / 解释节点及其邻接关系 |
| `/graphify affected "X"` | Reverse traversal — what does X impact? / 反向遍历：X 影响了谁？ |
| `graphify update .` | Incremental update, no LLM cost / 增量更新，无 LLM 开销 |

---

## Query Examples / 查询示例

```bash
# Architecture / 架构理解
/graphify query "agent core execution flow"              # agent 核心执行流程
/graphify query "how does the permission system work"    # 权限系统如何工作
/graphify query "how are LLM providers registered"       # LLM 提供者如何注册

# Dependency tracing / 依赖追踪
/graphify path "NewAgent" "Session"
/graphify path "PluginSelector" "Hook"
/graphify path "Provider" "Tool"

# Impact analysis / 影响分析
/graphify explain "contains()"
/graphify affected "ProviderSelector"
/graphify explain "SlashCommandController"
```

---

## Workflows / 工作流

### New Feature Development / 开发新功能

**1. Understand the landscape / 了解现有架构** — where does the feature fit?

```bash
/graphify query "how does X work"          # understand existing system / 理解现有系统
/graphify path "FeatureArea" "EntryPoint"  # trace the call chain / 追踪调用链
```

**2. Find integration points / 找到集成点** — what to wire into?

```bash
/graphify explain "ProviderSelector"       # see what connects to it / 查看连接关系
/graphify affected "HookEngine"            # who depends on this? / 谁依赖它？
```

**3. Discover similar patterns / 参考已有模式** — follow existing conventions

```bash
/graphify path "NewProvider" "Registry"    # how other providers register / 其他 provider 的注册方式
/graphify query "tool registration flow"   # find the registration pattern / 找到注册模式
```

**4. After implementation / 实现后** — update the graph

```bash
graphify update .    # incremental, no LLM cost / 增量更新，无 LLM 开销
```

### Bug Fixing / 修复 Bug

**1. Locate the bug site / 定位 Bug** — trace from symptom to source

```bash
/graphify query "permission denial flow"
/graphify path "ErrorMessage" "Handler"
```

**2. Assess blast radius / 评估影响范围** — who else depends on this code?

```bash
/graphify affected "BuggyFunction()"       # what calls this? / 谁调用了它？
/graphify explain "BuggyComponent"         # what's coupled? / 什么与之耦合？
```

**3. Find related test coverage / 查找相关测试**

```bash
/graphify path "BuggyFunction" "Test"      # are there tests for this? / 该路径有测试吗？
/graphify query "tests for permission"     # find test communities / 找到相关测试社区
```

**4. Verify fix is complete / 验证修复完整** — check all callers

```bash
/graphify affected "FixedSymbol"           # review every caller / 审查所有调用方
```

### Refactoring / 重构

**1. Measure coupling / 评估耦合度** — is it safe to change?

```bash
/graphify explain "GodNode"                # high edge count = risky / 边数多 = 风险高
/graphify affected "PublicAPI"             # downstream consumers / 下游消费者
```

**2. Plan the cut points / 规划切割点**

```bash
/graphify path "ModuleA" "ModuleB"         # find the dependency chain / 找到依赖链
```

**3. After refactoring / 重构后** — rebuild

```bash
graphify update .    # AST-only, catches moved/renamed symbols / 仅 AST，捕获移动/重命名
```

---

## Visual Exploration / 可视化探索

```bash
# Interactive graph in browser / 在浏览器中打开交互式图谱
xdg-open graphify-out/graph.html
```

Click nodes to explore connections, filter by community, search by name.
点击节点查看连接，按社区过滤，按名称搜索。

---

## Maintenance / 维护

After code changes / 代码变更后：

```bash
# Incremental update (AST only, free) / 增量更新（仅 AST，免费）
graphify update .

# Re-cluster and re-label communities (uses LLM) / 重新聚类和命名社区（使用 LLM）
source ~/.claude_glm && env ANTHROPIC_API_KEY="$ANTHROPIC_AUTH_TOKEN" \
  ANTHROPIC_BASE_URL="$ANTHROPIC_BASE_URL" \
  GRAPHIFY_VIZ_NODE_LIMIT=10000 \
  graphify cluster-only .

# Label new communities only / 仅命名新社区
source ~/.claude_glm && env ANTHROPIC_API_KEY="$ANTHROPIC_AUTH_TOKEN" \
  ANTHROPIC_BASE_URL="$ANTHROPIC_BASE_URL" \
  graphify label . --backend=claude
```

---

## Output Files / 输出文件

| File / 文件 | Description / 说明 |
|------|-------------|
| `graph.html` | Interactive visualization (5.8 MB) / 交互式可视化 |
| `graph.json` | Full graph data (6.5 MB) / 完整图谱数据 |
| `GRAPH_REPORT.md` | Analysis report — god nodes, surprising connections, communities / 分析报告 |
| `manifest.json` | File manifest with hashes / 文件清单及哈希 |

---

## Top 5 God Nodes / 核心节点 Top 5

1. `contains()` — 211 edges / 边（cross-community hub / 跨社区枢纽）
2. `PluginSelector` — 72 edges / 边
3. `ProviderSelector` — 60 edges / 边
4. `NewEngine()` — 58 edges / 边
5. `NewData()` — 55 edges / 边
