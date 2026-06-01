# 包结构详解

本文档详细说明 Gen Code 的包组织结构和各包的职责。

---

## 项目包全景图

```
github.com/genai-io/gen-code/
├── cmd/gen/                       # CLI 入口
│   ├── main.go                    #   根命令、标志解析、提供商导入
│   ├── agent.go                   #   agent run 子命令
│   ├── mcp.go                     #   mcp 子命令
│   ├── plugin.go                  #   plugin 子命令
│   └── inspector.go               #   inspector 子命令
│
├── internal/
│   ├── app/                       # 应用外壳（Bubble Tea TUI）
│   │   ├── run.go                 #   统一入口（打印/交互模式路由）
│   │   ├── model.go               #   根 Bubble Tea Model
│   │   ├── services.go            #   17 个领域服务注入
│   │   ├── view.go                #   终端渲染（View）
│   │   ├── init.go                #   基础设施初始化
│   │   ├── env.go                 #   环境状态
│   │   ├── hooks.go               #   启动钩子
│   │   ├── agent.go               #   Agent 通信、出站轮询
│   │   ├── model_lifecycle.go     #   构造、选项、关闭
│   │   ├── model_session.go       #   会话保存/加载
│   │   ├── model_agent_events.go  #   Agent 事件回调
│   │   ├── model_compact.go       #   对话压缩
│   │   ├── model_turn_queue.go    #   轮次队列排空
│   │   ├── model_scrollback.go    #   滚动回看提交
│   │   ├── update.go              #   Update 分发
│   │   ├── update_keys.go         #   键盘处理
│   │   ├── update_submit.go       #   提交处理
│   │   ├── update_input_effects.go#   输入副作用（中断等）
│   │   ├── update_command.go      #   斜杠命令环境
│   │   ├── conv/                  #   对话渲染状态
│   │   │   ├── conversation.go    #       对话模型
│   │   │   ├── message.go         #       消息类型
│   │   │   └── update.go          #       事件→渲染路由
│   │   ├── input/                 #   输入处理
│   │   │   ├── textarea.go        #       文本输入组件
│   │   │   ├── selector.go        #       选择器组件
│   │   │   ├── slash_command.go   #       斜杠命令控制器
│   │   │   └── approval.go        #       权限批准桥接
│   │   ├── trigger/               #   系统触发器
│   │   │   ├── cron.go            #       Cron 定时器
│   │   │   └── async_hook.go      #       异步钩子轮询
│   │   ├── hub/                   #   事件总线
│   │   │   └── hub.go             #       进程内 Pub/Sub
│   │   └── kit/                   #   TUI 助手
│   │       ├── style.go           #       样式常量
│   │       └── dim.go             #       变暗效果
│   │
│   ├── core/                      # 稳定契约（核心接口）
│   │   ├── agent.go               #   Agent 接口、Config、Event、Result
│   │   ├── agent_impl.go          #   Agent 默认实现（Run 循环）
│   │   ├── llm.go                 #   LLM 接口、InferRequest/Response、Chunk
│   │   ├── tool.go                #   Tool、ToolSchema、Tools 接口
│   │   ├── message.go             #   Message、ChatMessage、ToolCall/Result
│   │   ├── system.go              #   System 接口、Section
│   │   ├── section.go             #   内置系统提示片段
│   │   └── digest.go              #   内容寻址摘要
│   │
│   ├── agent/                     # Agent 构建
│   │   ├── session.go             #   会话对接受口、权限适配器
│   │   └── lifecycle.go           #   生命周期处理器
│   │
│   ├── llm/                       # LLM 提供商
│   │   ├── types.go               #   Provider 接口、ProviderStore
│   │   ├── store.go               #   全局提供商注册表
│   │   ├── registry.go            #   空白导入注册
│   │   ├── factory.go             #   客户端工厂
│   │   ├── cost.go                #   成本追踪
│   │   ├── log.go                 #   请求/响应日志
│   │   ├── anthropic/             #   Anthropic Claude 实现
│   │   ├── openai/                #   OpenAI GPT/o-series 实现
│   │   ├── google/                #   Google Gemini 实现
│   │   ├── moonshot/              #   Moonshot Kimi 实现
│   │   ├── alibaba/               #   Alibaba DashScope 实现
│   │   ├── minmax/                #   MiniMax 实现
│   │   ├── bigmodel/              #   Z.ai GLM 实现
│   │   └── deepseek/              #   DeepSeek 实现
│   │
│   ├── tool/                      # 工具系统
│   │   ├── schema_base.go         #   12 个内置工具 Schema
│   │   ├── tool.go                #   工具注册/初始化
│   │   ├── fs/                    #   文件系统工具实现
│   │   │   ├── read.go            #       Read
│   │   │   ├── write.go           #       Write
│   │   │   ├── edit.go            #       Edit
│   │   │   ├── bash.go            #       Bash
│   │   │   ├── glob.go            #       Glob
│   │   │   └── grep.go            #       Grep
│   │   ├── web/                   #   Web 工具实现
│   │   │   ├── fetch.go           #       WebFetch
│   │   │   └── search.go          #       WebSearch
│   │   ├── tasktools/             #   任务管理工具
│   │   │   ├── task_output.go     #       TaskOutput
│   │   │   └── task_stop.go       #       TaskStop
│   │   ├── agent/                 #   Agent 启动工具
│   │   ├── skill/                 #   技能工具适配器
│   │   ├── perm/                  #   权限模型和批准门控
│   │   ├── mode/                  #   工具执行模式
│   │   └── registry/              #   工具注册表实现
│   │
│   ├── session/                   # 会话管理
│   │   ├── metadata.go            #   会话元数据
│   │   ├── paths.go               #   路径管理
│   │   ├── convert.go             #   核心/转录本类型转换
│   │   └── transcript/            #   转录本存储
│   │       ├── record.go          #       记录类型
│   │       ├── store.go           #       文件系统存储
│   │       ├── projection.go      #       投影
│   │       └── render.go          #       可渲染视图
│   │
│   ├── skill/                     # 技能管理
│   │   ├── registry.go            #   技能注册表
│   │   ├── loader.go              #   文件加载器
│   │   ├── store.go               #   状态持久化
│   │   └── types.go               #   技能类型定义
│   │
│   ├── subagent/                  # 子 Agent 管理
│   │   ├── registry.go            #   子 Agent 注册表
│   │   ├── loader.go              #   文件加载器
│   │   ├── sandbox.go             #   沙箱隔离
│   │   ├── exec.go                #   执行引擎
│   │   └── signal.go              #   信号处理
│   │
│   ├── hook/                      # 钩子系统
│   │   ├── engine.go              #   钩子引擎
│   │   ├── matcher.go             #   事件匹配器
│   │   ├── registry.go            #   钩子注册表
│   │   ├── exec_command.go        #   命令执行器
│   │   ├── exec_http.go           #   HTTP 执行器
│   │   ├── exec_llm.go            #   LLM 执行器
│   │   ├── exec_agent.go          #   Agent 执行器
│   │   └── store.go               #   钩子状态存储
│   │
│   ├── plugin/                    # 插件管理
│   │   ├── registry.go            #   插件注册表
│   │   ├── loader.go              #   插件加载器
│   │   ├── install.go             #   安装/卸载
│   │   └── marketplace.go         #   市场集成
│   │
│   ├── command/                   # 斜杠命令
│   │   ├── registry.go            #   命令注册表
│   │   ├── builtin.go             #   内置命令
│   │   └── loader.go              #   自定义命令加载
│   │
│   ├── mcp/                       # MCP 客户端
│   │   ├── client.go              #   MCP 客户端
│   │   ├── registry.go            #   服务器注册表
│   │   ├── caller.go              #   工具调用转发
│   │   └── hooks.go               #   钩子集成
│   │
│   ├── task/                      # 后台任务
│   │   ├── bash.go                #   Bash 后台任务
│   │   ├── agent.go               #   Agent 后台任务
│   │   ├── output.go              #   输出持久化
│   │   ├── hooks.go               #   任务事件钩子
│   │   └── tracker/               #   任务状态追踪
│   │       ├── store.go           #       状态存储
│   │       └── service.go         #       后台服务
│   │
│   ├── cron/                      # Cron 调度
│   │   ├── service.go             #   调度服务
│   │   ├── store.go               #   任务存储
│   │   └── types.go               #   类型定义
│   │
│   ├── search/                    # Web 搜索后端
│   │   ├── exa.go                 #   Exa 实现
│   │   ├── tavily.go              #   Tavily 实现
│   │   ├── brave.go               #   Brave 实现
│   │   └── serper.go              #   Serper 实现
│   │
│   ├── setting/                   # 设置管理
│   │   ├── settings.go            #   设置数据类型
│   │   ├── loader.go              #   配置加载/合并
│   │   ├── permissions.go         #   权限模式
│   │   └── modes.go               #   操作模式
│   │
│   ├── identity/                  # 身份/人格
│   │   ├── registry.go            #   身份注册表
│   │   ├── loader.go              #   模板加载
│   │   └── paths.go               #   路径管理
│   │
│   ├── inspector/                 # 会话检查器
│   │   └── server.go              #   嵌入式 HTTP 服务器
│   │
│   ├── reminder/                  # 运行时提醒
│   │   └── queue.go               #   提醒队列
│   │
│   ├── worktree/                  # Git 工作树
│   │   └── worktree.go            #   工作树操作
│   │
│   ├── log/                       # 日志
│   │   └── log.go                 #   Zap + Lumberjack
│   │
│   ├── secret/                    # 密钥管理
│   │   └── secret.go              #   凭证助手
│   │
│   ├── filecache/                 # 文件缓存
│   │   └── cache.go               #   缓存/恢复
│   │
│   ├── markdown/                  # Markdown 解析
│   │   └── frontmatter.go         #   前置元数据提取
│   │
│   ├── image/                     # 图片处理
│   │   └── process.go             #   图片编解码
│   │
│   └── proc/                      # 进程管理
│       └── proc.go                #   跨平台进程组
│
├── docs/                          # 文档
│   ├── architecture.md            #   架构总览
│   ├── concepts/                  #   跨领域概念
│   ├── packages/                  #   分包的详细设计文档
│   ├── reference/                 #   参考手册
│   ├── guides/                    #   用户指南
│   ├── operations/                #   构建/测试/发布
│   └── cn/                        #   中文文档（本文档）
│
├── Makefile                       # 构建脚本
├── go.mod                         # Go 模块定义
├── go.sum                         # 依赖校验和
├── LICENSE                        # Apache 2.0
└── README.md                      # 项目说明
```

---

## 依赖关系图

```
cmd ───────────────► app ─────────► feature ────────► core ──────► infrastructure
│                     │               │                 │              │
│  cmd/gen/           │  internal/    │  internal/      │  internal/   │  internal/
│  ├─ main.go         │  app/         │  ├─ agent/      │  core/       │  ├─ log/
│  ├─ agent.go        │  ├─ run.go    │  ├─ llm/        │  ├─ agent.go │  ├─ secret/
│  ├─ mcp.go          │  ├─ model.go  │  ├─ tool/       │  ├─ llm.go   │  └─ ...
│  ├─ plugin.go       │  ├─ view.go   │  ├─ session/    │  ├─ tool.go  │
│  └─ inspector.go    │  ├─ conv/     │  ├─ skill/      │  ├─ message.go│
│                     │  ├─ input/    │  ├─ subagent/   │  └─ system.go │
│                     │  ├─ trigger/  │  ├─ hook/       │               │
│                     │  └─ hub/      │  ├─ plugin/     │               │
│                     │               │  ├─ command/    │               │
│                     │               │  ├─ mcp/        │               │
│                     │               │  ├─ task/       │               │
│                     │               │  ├─ cron/       │               │
│                     │               │  ├─ search/     │               │
│                     │               │  ├─ setting/    │               │
│                     │               │  ├─ identity/   │               │
│                     │               │  └─ ...         │               │
```

**规则**：
- 依赖只能从上层指向下层，不可反向
- `core` 层不依赖任何 feature 或 app 层包
- `feature` 层包之间可以有非循环依赖
- `infrastructure` 层不依赖其他层

---

## 关键包详解

### cmd/gen — 命令行入口

**依赖方向**：cmd → app → feature

`main.go` 是唯一的程序入口：
- 使用 [Cobra](https://github.com/spf13/cobra) 构建 CLI
- 通过空白导入注册所有 LLM 提供商
- 加载 `.env` 文件（`godotenv`）
- 初始化日志（`GEN_DEBUG=1` 启用）
- 设置应用版本（用于会话记录）

**CLI 标志**：
| 标志 | 简写 | 类型 | 说明 |
|------|------|------|------|
| `--print` | `-p` | string | 非交互打印模式 |
| `--continue` | `-c` | bool | 恢复最近会话 |
| `--resume` | `-r` | bool/string | 恢复指定会话 |
| `--plugin-dir` | | string | 插件目录 |

**stdin 管道处理**：`readStdin()` 检测 stdin 是否为管道，是则读取全部内容。

### internal/app — TUI 应用外壳

**依赖方向**：app → feature → core

这是最复杂的包，实现了完整的 Bubble Tea TUI。

**Model 组合**：
```go
type Model struct {
    // 子模型
    conv       conv.Model       // 对话视图
    userInput  input.Model      // 用户输入
    env        Env              // 环境状态
    services   services         // 领域服务
    
    // 事件和状态
    stream     Stream           // 流式状态
    systemInput SystemInput     // 系统触发队列
    mainEvents chan Event       // Hub 事件通道
}
```

**服务注入**（`services.go`）：
```go
type services struct {
    Setting   setting.Service   // 设置和权限
    LLM       llm.Service       // LLM 提供商
    Tool      tool.Service      // 工具注册表
    Hook      hook.Engine       // 钩子引擎
    Session   session.Service   // 会话持久化
    Skill     skill.Service     // 技能注册表
    Subagent  subagent.Service  // 子 Agent
    Command   command.Service   // 斜杠命令
    Task      task.Service      // 后台任务
    Tracker   tracker.Service   // 任务追踪
    Cron      cron.Service      // 定时任务
    MCP       mcp.Service       // MCP 客户端
    Plugin    plugin.Service    // 插件注册表
    Agent     agent.Service     // Agent 工厂
    Identity  identity.Service  // 身份注册表
    Reminder  reminder.Queue    // 提醒队列
}
```

### internal/agent — Agent 构建

- **`session.go`**：Agent 会话对接，权限适配器，Inbox/Outbox 管理
- **`lifecycle.go`**：生命周期处理器，任务完成通知

### internal/llm — LLM 提供商系统

- **`types.go`**：Provider 接口和 ProviderStore
- **`store.go`**：全局提供商注册表
- **`registry.go`**：通过空白导入的自动注册
- **`factory.go`**：客户端工厂函数
- **`cost.go`**：Token 成本计算
- **`log.go`**：请求/响应日志

每个提供商实现包（`anthropic/`、`openai/` 等）包含：
1. `init()` 函数：注册 Provider
2. Provider 实现：返回模型列表、创建客户端
3. Infer 实现：消息格式转换、流式响应处理

### internal/tool — 工具系统

- **`schema_base.go`**：12 个工具的 JSON Schema 定义（最大的源文件之一）
- **`fs/`**：文件系统工具实现，每个工具一个文件
- **`web/`**：WebFetch 和 WebSearch 实现
- **`tasktools/`**：任务管理工具
- **`perm/`**：权限门控，三种模式（ask/auto-accept/plan）
- **`registry/`**：工具注册表，动态添加/移除

### internal/session — 会话持久化

- **`metadata.go`**：会话元数据（ID、开始时间、模型、版本、工作区）
- **`paths.go`**：会话文件路径管理（`~/.gen/projects/<project>/`）
- **`convert.go`**：`core.Message` ↔ 转录本记录的转换
- **`transcript/record.go`**：转录本记录类型
- **`transcript/store.go`**：JSON 文件存储
- **`transcript/projection.go`**：会话投影（子集时间范围等）
- **`transcript/render.go`**：转换为可渲染视图

---

## 技术栈一览

| 用途 | 技术 |
|------|------|
| CLI | Cobra + 自定义标志 |
| TUI | Bubble Tea + Bubbles + Lip Gloss + Glamour |
| LLM | Anthropic SDK + OpenAI SDK + Google GenAI SDK |
| HTTP | net/http 标准库 |
| 日志 | Zap + Lumberjack |
| YAML | gopkg.in/yaml.v3 |
| Shell 解析 | mvdan.cc/sh |
| Diff | hexops/gotextdiff |
| Glob | bmatcuk/doublestar |
| HTML→MD | goquery + html-to-markdown |
| 环境变量 | godotenv |
| 终端宽度 | mattn/go-runewidth |
| Concurrency | Go 标准库（goroutine, channel, sync/atomic） |
