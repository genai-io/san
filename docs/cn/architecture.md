# 架构详解

## 五层架构模型

Gen Code 采用严格的**五层架构**，依赖方向只能从上层指向下层：

```
cmd  →  app  →  feature  →  core  →  infrastructure
```

每层职责明确，禁止反向依赖：

### 第 1 层：cmd（命令行入口）

- **位置**：[`cmd/gen/`](../../cmd/gen/)
- **职责**：CLI 入口点，标志解析，子命令注册
- **核心文件**：
  - `main.go` — 使用 Cobra 构建 CLI，注册子命令和全局标志
  - `agent.go` — `agent run` 子命令（无头 Agent）
  - `inspector.go` — 会话检查器（HTTP 服务器）
  - `mcp.go` — MCP 服务器管理
  - `plugin.go` — 插件管理

**CLI 标志**：
| 标志 | 简写 | 说明 |
|------|------|------|
| `--print` | `-p` | 非交互打印模式 |
| `--continue` | `-c` | 恢复最近的会话 |
| `--resume` | `-r` | 选择并恢复历史会话 |
| `--plugin-dir` | | 从指定目录加载插件 |

**LLM 提供商通过空白导入注册**（`init()` 函数自动注册）：
```go
import (
    _ "github.com/genai-io/gen-code/internal/llm/anthropic"
    _ "github.com/genai-io/gen-code/internal/llm/openai"
    _ "github.com/genai-io/gen-code/internal/llm/google"
    // ... 其他提供商
)
```

### 第 2 层：app（应用外壳）

- **位置**：[`internal/app/`](../../internal/app/) 及其子包
- **职责**：Bubble Tea TUI 外壳，模型组合，事件路由，生命周期管理
- **核心理念**：MVU（Model-View-Update）模式

**关键文件与职责**：

| 文件 | 职责 |
|------|------|
| `model.go` | 根 Bubble Tea Model：组合子模型（用户输入、Hub 事件、系统触发器、对话、环境、服务） |
| `services.go` | 服务注入结构体：包含 16 个领域单例 |
| `run.go` | 统一入口：路由到打印模式或交互 TUI |
| `model_lifecycle.go` | 构造、选项应用、关闭 |
| `model_session.go` | 会话保存/加载、任务存储 |
| `model_agent_events.go` | Agent 出站事件回调 |
| `model_compact.go` | 自动和手动对话压缩 |
| `model_turn_queue.go` | 入站消息排空、提示注入、停止钩子门控 |
| `view.go` | 终端渲染（Bubble Tea View） |
| `update*.go` | 按键/权限/命令/提交/调整大小/输入处理 |

**子包**：
- [conv/](../../internal/app/conv/) — 对话渲染状态（流式文本、工具块、滚动回看）
- [input/](../../internal/app/input/) — 文本输入、选择器、权限桥接、斜杠命令分发
- [trigger/](../../internal/app/trigger/) — Cron 定时器和异步钩子轮询
- [hub/](../../internal/app/hub/) — 进程内发布/订阅事件总线
- [kit/](../../internal/app/kit/) — 共享 TUI 助手（样式、变暗）

### 第 3 层：feature（业务逻辑）

包含 20+ 个领域包，每个包负责一个独立的功能域：

| 包 | 职责 |
|---|------|
| `agent/` | Agent 构建、权限适配器、会话对接受口 |
| `llm/` | 提供商注册表、客户端工厂、成本追踪、日志、流式处理 |
| `tool/` | 内置工具注册表、Schema、适配器、执行、权限包装 |
| `tool/fs/` | 文件系统工具：Read、Write、Edit、Bash、Glob、Grep |
| `tool/web/` | Web 工具：WebFetch、WebSearch |
| `tool/tasktools/` | 任务跟踪工具：TaskCreate、TaskGet、TaskList、TaskUpdate |
| `tool/perm/` | 权限模型和批准门控 |
| `tool/registry/` | 工具注册表实现 |
| `session/` | 会话元数据、路径管理、核心/转录本类型转换 |
| `session/transcript/` | 转录本记录、文件系统存储、投影、可渲染视图 |
| `subagent/` | 子 Agent 注册表、加载、执行、沙箱、结果收集 |
| `hook/` | 钩子引擎、匹配器、注册表、执行器（命令、HTTP、LLM） |
| `skill/` | 技能注册表、YAML/Markdown 加载、激活/停用 |
| `plugin/` | 插件注册表、加载、安装、市场集成 |
| `command/` | 斜杠命令注册表 |
| `mcp/` | MCP 客户端、注册表、调用器、钩子集成 |
| `cron/` | Cron 调度、存储、服务 |
| `task/` | 后台任务管理、Bash/Agent 执行、输出持久化 |
| `search/` | Web 搜索后端：Exa、Tavily、Brave、Serper |
| `setting/` | 设置、权限、操作模式、配置加载/合并 |
| `identity/` | 身份/人格注册表、模板解析、路径管理 |
| `inspector/` | 会话转录本检查器 |
| `worktree/` | Git 工作树操作 |
| `reminder/` | 运行时提醒队列 |

### 第 4 层：core（稳定契约）

- **位置**：[`internal/core/`](../../internal/core/)
- **职责**：定义核心抽象接口，不包含实现
- **核心接口**：`Agent`、`LLM`、`Tool`、`Tools`、`System`、`Message`、`Event`
- **设计原则**：
  - 接口只定义能力，不定义实现
  - 所有通信通过 Go Channel 进行
  - Hook 是应用层概念，不在 core 中

### 第 5 层：infrastructure（基础设施）

| 包 | 职责 |
|---|------|
| `log/` | 结构化日志（Zap + Lumberjack 轮转） |
| `secret/` | 密钥/凭证助手 |
| `filecache/` | 文件缓存/恢复 |
| `markdown/` | Markdown 前置元数据解析 |
| `image/` | 图片处理 |
| `proc/` | 跨平台进程组和信号处理 |

---

## 运行时模型

Gen Code 的运行时是一个**事件驱动的 Agent 循环**，运行在 Bubble Tea TUI 框架内。

### 三个输入源

```
  来源 1（用户）         来源 2（Agent）         来源 3（系统）
  提交                    子 Agent 完成           Cron 触发
  斜杠命令                sendMsg                异步钩子回调
  模态框响应              selfInject             文件监视器
           \                   |                      /
            \__________________|_____________________/
                               |
                        sendToAgent()
                               v
                ┌────────────────────────────┐
                │           Agent            │
                │   Inbox  →  Run  →  Outbox │
                │   LLM  ↔  Tool  ↔  LLM ...│
                └──────────────┬─────────────┘
                               |
                               v
                        TUI 观察
                (对话视图、权限桥接、
                 令牌追踪、状态栏)
```

### Agent 循环的三个阶段

```
Phase 1 — WAIT（阻塞等待）
  └─ 阻塞在 Inbox 上，等待消息
  └─ 收到 SigStop 或 ctx.Done() → 触发 OnStop 钩子并返回

Phase 2 — DRAIN（非阻塞排空）
  └─ 排空 Inbox 中所有累积的消息
  └─ 所有消息追加到对话历史

Phase 3 — THINK + ACT（推理-行动循环）
  └─ 循环：LLM 推理 → 工具执行 → LLM 推理 → ...
  └─ 每轮之间非阻塞检查 Inbox 中的新消息
  └─ 发出流式块和工具结果到 Outbox
  └─ 循环直到 LLM 返回 end_turn
  └─ 然后回到 Phase 1
```

### Bubble Tea MVU 循环

TUI 基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea) 框架，遵循 MVU 模式：

- **Model（模型）**：应用状态
- **View（视图）**：`View()` 方法将模型渲染为字符串
- **Update（更新）**：`Update(msg)` 方法处理消息并返回新模型 + 命令

三种 Bubble Tea 原语驱动一切：
- **`tea.Msg`**：进入模型的事件（按键、窗口调整、自定义消息）
- **`Update(msg)`**：纯函数，变异模型并返回 `tea.Cmd`
- **`tea.Cmd`**：框架运行的函数，返回值作为新 `tea.Msg` 注入回 Update

### 终端渲染的双面模型

```
  终端原生滚动回看区域            Bubble Tea 重绘区域
  （可以通过 tea.Println         （底部 N 行，每次 Update
   写入的行，可向上滚动           重新绘制；重绘间内容丢弃）
   查看；永远不会被重绘）
```

**流式渲染**时，正在进行的文本在重绘区域中逐 Token 更新。**流结束时**，`CommitMessages` 通过 `tea.Println` 将完整消息提升到滚动回看区域。

---

## 服务注入

`internal/app/services.go` 中的 `services` 结构体注入了 16 个领域单例：

```go
type services struct {
    Setting   // 设置管理
    LLM       // LLM 提供商
    Tool      // 工具注册表
    Hook      // 钩子引擎
    Session   // 会话管理
    Skill     // 技能注册表
    Subagent  // 子 Agent 注册表
    Command   // 斜杠命令注册表
    Task      // 后台任务
    Tracker   // 任务追踪
    Cron      // Cron 调度
    MCP       // MCP 客户端
    Plugin    // 插件注册表
    Agent     // Agent 工厂
    Identity  // 身份注册表
    Reminder  // 提醒队列
}
```

---

## 配置目录布局

用户级（`~/.gen/`）：
```
providers.json    # 提供商连接和当前模型
settings.json     # 权限、钩子、环境、身份
skills.json       # 技能状态
identities/       # 自定义人格
skills/           # 自定义技能定义
agents/           # 自定义 Agent 定义
commands/         # 自定义斜杠命令
plugins/          # 已安装的插件
projects/         # 会话转录本 + 索引
```

项目级（`.gen/`）：
```
settings.json      # 权限、钩子、禁用的工具
mcp.json           # MCP 服务器定义
identities/*.md    # 项目范围的人格（覆盖用户级）
agents/*.md        # 子 Agent 定义
skills/*/SKILL.md  # 技能
commands/*.md      # 斜杠命令
```
