# 扩展模型详解

Gen Code 支持用户无需修改 Go 代码即可扩展功能。有**四种扩展原语**加一个**插件源**。

---

## 扩展原语概览

| 原语 | 说明 | 存放位置 |
|------|------|----------|
| **Skill（技能）** | Markdown 文件，模型可感知或通过斜杠命令调用 | `~/.gen/skills/<name>/SKILL.md` |
| **Subagent（子 Agent）** | Markdown 定义的 Agent 类型，具有自己的系统提示和工具子集 | `~/.gen/agents/<name>.md` |
| **Slash Command（斜杠命令）** | Markdown 文件，注入参数化提示 | `~/.gen/commands/<name>.md` |
| **Hook（钩子）** | Shell 命令、HTTP 端点、LLM 调用或进程内回调 | `settings.json` 的 `hooks` 字段 |

加上入站方向：

| 原语 | 说明 |
|------|------|
| **Tool（工具）** | Agent 可调用的能力。内置或由 MCP 贡献 |

---

## 1. Skill（技能）

技能是 Markdown 文件，模型可以：
- **被动感知**：内容被注入到系统提示中
- **主动调用**：通过 `/skill-name` 斜杠命令触发

### 文件格式

```markdown
---
name: my-skill
description: 对技能的一句话描述
namespace: optional
allowed-tools: [Read, Glob]  # 可选的工具白名单
---

技能的正文内容，可以是说明、指令或任何上下文。
```

### 技能注册表

实现在 [`internal/skill/`](../../internal/skill/)：
- 扫描 `~/.gen/skills/` 和 `<project>/.gen/skills/` 目录
- 支持 Claude Code 兼容的 `.md` 技能格式
- 可通过 `skills.json` 启用/禁用

---

## 2. Subagent（子 Agent）

子 Agent 是通过 `Agent` 工具启动的专用 Agent，具有受限的工具集和自定义系统提示。

### 文件格式

```markdown
---
name: code-reviewer
description: 代码审查 Agent
allowed-tools: [Read, Glob, Grep]
---

你是一个代码审查专家。你的任务是对代码进行全面的审查...
```

### 子 Agent 注册表

实现在 [`internal/subagent/`](../../internal/subagent/)：
- 加载用户级和项目级的 Agent 定义
- 通过 `allowed-tools` 限制工具访问
- 支持沙箱隔离（Git worktree）
- 结果通过任务系统收集并通知父 Agent

### 执行流程

```
父 Agent 调用 Agent tool
  → 创建子 Agent（受限工具集）
  → 子 Agent 在自己的 goroutine 中运行
  → 子 Agent 完成后通过 hub.Publish 通知
  → 父 Agent 收到通知并继续
```

---

## 3. Slash Command（斜杠命令）

斜杠命令是用户在输入框中输入 `/` 前缀调用的命令。

### 内置命令

| 命令 | 功能 |
|------|------|
| `/model` | 选择和切换 LLM 模型 |
| `/search` | 切换搜索引擎 |
| `/identity` | 切换人格/身份 |
| `/think` | 切换思考模式预算 |
| `/compact` | 手动压缩对话上下文 |
| `/clear` | 清除对话历史 |
| `/help` | 显示帮助 |
| `/agents` | 管理子 Agent |
| `/skills` | 管理技能 |
| `/mcp` | 管理 MCP 服务器 |
| `/plugins` | 管理插件 |
| `/hooks` | 管理钩子 |

### 自定义命令

存放在 `~/.gen/commands/*.md`：

```markdown
---
name: deploy
description: 部署当前项目
argument-hint: <环境>
---

执行部署流程：
1. 运行测试
2. 构建项目
3. 部署到 $ARGUMENTS 环境
```

实现在 [`internal/command/`](../../internal/command/)。

---

## 4. Hook（钩子）

钩子是特定事件发生时的回调，定义在 `settings.json` 的 `hooks` 字段中。

### 钩子类型

| 类型 | 说明 |
|------|------|
| **Command** | 执行 Shell 命令 |
| **HTTP** | 发送 HTTP 请求 |
| **LLM** | 调用 LLM（用于提示检查等） |
| **Agent** | 启动子 Agent 处理 |

### 触发事件

钩子可以绑定到 Agent 生命周期的任意事件：
- `PreInfer` — LLM 推理前
- `PostInfer` — LLM 推理后
- `PreTool` — 工具执行前
- `PostTool` — 工具执行后
- `OnTurn` — 轮次完成
- `OnStart` / `OnStop` — Agent 启动/停止
- 等等

### 钩子引擎

实现在 [`internal/hook/`](../../internal/hook/)：
- **Matcher** — 将钩子与事件匹配
- **Registry** — 管理钩子生命周期
- **Executors** — 命令、HTTP、LLM、Agent 四种执行器
- **Store** — 持久化钩子状态

---

## 5. MCP（Model Context Protocol）

MCP 允许外部进程向 Agent 提供工具。实现在 [`internal/mcp/`](../../internal/mcp/)。

### MCP 配置

定义在项目的 `.gen/mcp.json` 中：

```json
{
  "servers": {
    "weather": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-weather"]
    }
  }
}
```

### MCP 组件

- **Client** — 管理与 MCP 服务器的连接
- **Registry** — 跟踪已连接的服务器及其提供的工具
- **Caller** — 将 Agent 的工具调用转发到 MCP 服务器
- **Hook Integration** — 在 MCP 事件上触发钩子

### 工具生命周期

```
MCP Server 连接
  → 发现服务器提供的工具
  → 注册到 Agent 的 Tools 集合
  → Agent 调用时通过 MCP Client 转发

MCP Server 断开
  → 从 Tools 集合中移除相关工具
```

---

## 6. Plugin（插件）

**插件是上述原语的打包形式**。一个插件目录可以包含技能、Agent 定义、命令、MCP 服务器、钩子和环境变量。

```
┌────────────────────────────────────────┐
│                Plugin                  │
│   (一个目录，可版本锁定)                │
└──────┬────────┬───────┬──────┬─────────┘
       │        │       │      │
       ▼        ▼       ▼      ▼
   skill    agent   command   mcp    + hooks, env
```

### 插件加载

实现在 [`internal/plugin/`](../../internal/plugin/)：
- 扫描 `~/.gen/plugins/` 和项目 `.gen/plugins/`
- 将每个原语贡献推送到对应的消费包
- 通过 `--plugin-dir` 标志加载特定目录的插件

---

## 发现顺序（优先级）

扩展的解析遵循明确的优先级链（从高到低）：

```
project (.gen/<surface>/)
    > project plugins (.gen/plugins/*/...)
    > user (~/.gen/<surface>/)
    > user plugins (~/.gen/plugins/*/...)
    > Claude-compat (~/.claude/<surface>/, .claude/<surface>/)
```

高优先级条目按 **名称** 遮蔽低优先级条目。每个范围的 `IsEnabled` / 状态标志独立持久化。

---

## 前置元数据约定

所有 Markdown 定义的扩展原语共享相同的前置元数据结构：

```markdown
---
name: my-skill              # 必需
description: 一句话描述      # 选择器中显示
namespace: optional         # 仅技能支持
allowed-tools: [Read, Glob] # 工具子集（子 Agent 和技能）
argument-hint: <hint>       # 斜杠命令使用
---

文件正文成为提示/指令内容。
```

---

## 扩展包组织

```
internal/
├── skill/          # 技能注册表、加载、激活管理
├── subagent/       # 子 Agent 注册表、执行、沙箱
├── command/        # 斜杠命令注册表
├── hook/           # 钩子引擎、匹配器、执行器
├── plugin/         # 插件注册表、加载、市场
├── mcp/            # MCP 客户端、注册表、调用器
├── tool/           # 内置工具
│   └── agent/      # Agent 启动工具
│   └── skill/      # 技能工具适配器
├── setting/        # 权限模式、配置管理
└── identity/       # 身份/人格管理
```
