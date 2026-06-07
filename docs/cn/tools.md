# 工具系统详解

San 内置了一套完善的工具系统，Agent 通过调用这些工具与外部世界交互（读文件、执行命令、搜索网络等）。所有内置工具的 Schema 分布在 `internal/tool/` 下的三个文件：[`schema_base.go`](../../internal/tool/schema_base.go)（文件 / 系统 / Web 等基础工具）、[`schema_agent.go`](../../internal/tool/schema_agent.go)（Agent / Skill）、[`schema_task.go`](../../internal/tool/schema_task.go)（任务 / Cron / 工作树）。

---

## 工具接口

每个工具实现 `core.Tool` 接口，包含三个接口方法和一个执行方法：

```go
type Tool interface {
    Name() string                                              // 工具名称
    Description() string                                       // 工具描述（发送给 LLM）
    Schema() ToolSchema                                        // JSON Schema 定义
    Execute(ctx context.Context, input map[string]any) (string, error) // 执行逻辑
}
```

工具是**纯函数** —— 它们不知道钩子、权限或对话历史。Agent 循环处理拦截（通过钩子）和结果记录（通过 Message）。

---

## 内置工具一览

内置工具共约 **21 个**，按 schema 文件分为三组：基础工具（9 个，`schema_base.go`）、Agent/Skill（3 个，`schema_agent.go`）、任务/Cron/工作树（9 个，`schema_task.go`）。下面详述最常用的基础工具与 Agent 工具，其余在文末"其余内置工具"表中列出。

### 1. Read（读取文件）

读取本地文件系统上的文件。支持绝对和相对路径，默认读取前 2000 行，返回带行号的内容。

**参数**：
- `file_path`（string，必需）— 文件路径
- `offset`（integer，可选）— 起始行号
- `limit`（integer，可选）— 读取行数

**关键行为**：
- 可读取 PNG、JPG 等图片文件并可视化
- 读取目录或空文件返回系统提醒
- 支持 PDF 分页读取

---

### 2. Glob（文件名匹配）

快速文件模式匹配，适用于任何规模的代码库。

**参数**：
- `pattern`（string，必需）— Glob 模式，如 `**/*.go`
- `path`（string，可选）— 搜索目录，默认为当前工作目录

**关键行为**：
- 按修改时间降序排列结果
- 支持 `**` 递归匹配

---

### 3. Grep（内容搜索）

基于 ripgrep 的强大搜索工具。

**参数**：
- `pattern`（string，必需）— 正则表达式
- `path`（string，可选）— 搜索路径
- `glob`（string，可选）— 文件过滤
- `type`（string，可选）— 文件类型过滤（js、go、py 等）
- `output_mode`（enum：`content`、`files_with_matches`、`count`）
- `-i`（boolean）— 大小写不敏感（默认 true）
- `-n`（boolean）— 显示行号
- `context` / `-A` / `-B` / `-C`（integer）— 上下文行数
- `multiline`（boolean）— 多行模式
- `head_limit`（integer）— 输出行数限制
- `offset`（integer）— 跳过前 N 个结果

---

### 4. Edit（编辑文件）

精确字符串替换编辑文件。

**参数**：
- `file_path`（string，必需）— 文件路径
- `old_string`（string，必需）— 要被替换的文本
- `new_string`（string，必需）— 替换后的文本
- `replace_all`（boolean，可选）— 替换所有匹配项

**关键行为**：
- 必须在对话中先使用 Read 工具读取过该文件
- `old_string` 必须是文件中的唯一匹配
- `replace_all: true` 可替换所有出现

---

### 5. Write（写入文件）

创建或覆盖文件。

**参数**：
- `file_path`（string，必需）— 文件路径
- `content`（string，必需）— 写入内容

**关键行为**：
- 覆盖已存在文件前必须先读取
- 优先使用 Edit 工具修改已有文件
- 禁止主动创建文档文件（*.md）

---

### 6. Bash（执行命令）

执行 Bash 命令并返回输出。

**参数**：
- `command`（string，必需）— 要执行的命令
- `description`（string，必需）— 命令描述
- `timeout`（integer，可选）— 超时（毫秒，最大 600000）
- `run_in_background`（boolean，可选）— 后台运行

**关键行为**：
- 工作目录为会话工作目录
- 禁止使用此工具运行 `cat`、`grep`、`find` 等 —— 优先使用专用工具
- 支持后台执行，完成时获得通知

---

### 7. WebFetch（网页获取）

获取 URL 内容并转换为 Markdown。

**参数**：
- `url`（string，必需）— 目标 URL
- `format`（string，可选）— 输出格式：markdown（默认）或 raw

**关键行为**：
- HTTP 自动升级为 HTTPS
- GitHub URL 建议使用 `gh` CLI
- 大内容会被截断

---

### 8. WebSearch（网页搜索）

执行网络搜索，返回标题、URL 和摘要。

**参数**：
- `query`（string，必需）— 搜索查询
- `num_results`（integer，可选）— 结果数（默认 10）
- `allowed_domains`（array，可选）— 限制域名白名单
- `blocked_domains`（array，可选）— 域名黑名单

**后端**：Exa（默认）、Tavily、Brave、Serper（通过 `/search` 命令切换）

---

### 9. AskUserQuestion（提问用户）

向用户提出带预设选项的问题。

**参数**：
- `question`（string，可选）— 单个问题文本
- `options`（array，可选，2-8 项）— 预设选项
- `questions`（array，可选）— 多个问题

---

### 10. Agent（启动子 Agent）

启动一个新的子 Agent 处理复杂多步骤任务。

**参数**：
- `description`（string，必需）— 任务描述
- `prompt`（string，必需）— 任务提示
- `subagent_type`（string，可选）— Agent 类型
- `model`（string，可选）— 模型覆盖
- `isolation`（string，可选）— 隔离模式（worktree）
- `run_in_background`（boolean，可选）— 后台运行

---

### 其余内置工具

除上述 10 个外，还有以下内置工具（定义在 `schema_agent.go` / `schema_task.go`）：

| 工具 | 说明 |
|------|------|
| `Skill` | 在主对话中执行某个技能（Skill） |
| `SendMessage` | 向一个已存在的子 Agent 追加后续消息 |
| `TaskCreate` | 创建任务以跟踪多步骤工作的进度 |
| `TaskGet` | 按 ID 获取任务详情（描述、状态、依赖） |
| `TaskUpdate` | 更新任务的状态、详情或依赖 |
| `TaskList` | 列出所有任务及其状态与依赖 |
| `CronCreate` | 按 cron 表达式调度一个定时提示 |
| `CronDelete` | 按 ID 取消一个定时任务 |
| `CronList` | 列出所有定时任务及其状态、下次触发时间 |
| `EnterWorktree` | 将当前对话切入一个 Git 工作树（隔离实验） |
| `ExitWorktree` | 退出工作树会话，回到原工作目录 |

---

## 工具注册表

工具注册表实现在 [`internal/tool/registry/`](../../internal/tool/registry/)，实现 `core.Tools` 接口：

```go
type Tools interface {
    Get(name string) Tool
    All() []Tool
    Add(tool Tool, caller string)
    Remove(name, caller string)
    Schemas() []ToolSchema
    SetObserver(fn func(ToolsChange))
}
```

### 动态工具管理

工具可以在运行时动态添加/移除：

- **MCP 服务器**连接时注册工具，断开时移除
- **Skills** 可以声明 `allowed-tools` 限制子 Agent 工具集
- **Parent Agent** 过滤子 Agent 的工具集
- **钩子**可以动态添加或移除工具

### 工具权限

工具权限通过 `tool.WithPermission` 包装器实现（[`internal/tool/perm/`](../../internal/tool/perm/)）：

- 三种权限模式：**ask**（询问）、**auto-accept**（自动接受）、**plan**（计划模式）
- 用户可以逐个工具配置权限级别
- 权限模式通过 `Shift+Tab` 切换

---

## 工具执行流程

```
Agent Loop
  │
  ├─ LLM 返回 tool_use
  │
  ├─ 发出 PreTool 事件 → Outbox
  │
  ├─ 权限检查（tool/perm）
  │   └─ 需要批准？ → 暂停等待用户
  │
  ├─ 执行钩子（PreTool 钩子）
  │
  ├─ Tool.Execute(ctx, input)
  │   └─ 返回 (result, error)
  │
  ├─ 构建 ToolResult
  │   └─ error → ToolResult{IsError: true}
  │
  ├─ 追加 ToolResult Message 到对话
  │
  ├─ 发出 PostTool 事件 → Outbox
  │
  └─ 继续 LLM 推理循环
```

---

## 工具目录组织

```
internal/tool/
├── schema_base.go       # 基础工具 Schema（Read/Glob/Grep/Edit/Write/Bash/WebFetch/WebSearch/AskUserQuestion）
├── schema_agent.go      # Agent / SendMessage / Skill 的 Schema
├── schema_task.go       # 任务 / Cron / 工作树工具的 Schema
├── registry.go          # 工具注册与初始化
├── agent/               # Agent 工具（子 Agent 启动）
├── fs/                  # 文件系统工具实现
│   ├── read.go
│   ├── write.go
│   ├── edit.go
│   ├── bash.go
│   ├── glob.go
│   └── grep.go
├── web/                 # Web 工具实现
│   ├── webfetch.go
│   └── websearch.go
├── tasktools/           # 任务跟踪工具
│   ├── trackercreate.go
│   ├── trackerget.go
│   ├── trackerlist.go
│   └── trackerupdate.go
├── perm/                # 权限门控
├── mode/                # 执行模式
├── registry/            # 注册表实现
└── skill/               # 技能工具适配器
```
