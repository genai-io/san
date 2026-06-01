# 核心接口详解

Gen Code 的所有核心抽象定义在 [`internal/core/`](../../internal/core/) 包中。这些是纯接口，不包含实现，遵循 Go 的"定义接口于消费方"最佳实践。

---

## 1. Agent（智能体）

**文件**：[`internal/core/agent.go`](../../internal/core/agent.go)

Agent 是系统的核心抽象 —— 一个能够推理和行动的自治实体。只具备三种能力：

1. **System** — WHO：定义了 Agent 的身份（可组合、可变的身份）
2. **Tools** — WHAT：Agent 可以做什么（唯一的行动原语）
3. **Inbox/Outbox** — HOW：Agent 如何与外部通信（Go Channel）

```go
type Agent interface {
    ID() string
    System() System
    Tools() Tools

    // 入站消息通道：外部世界向 Agent 发送消息
    Inbox() chan<- Message

    // 出站事件通道：Agent 向外部世界发出事件
    Outbox() <-chan Event

    // 对话历史快照（浅拷贝）
    Messages() []Message
    SetMessages(msgs []Message)

    // 追加消息到对话并触发 OnMessage 钩子
    Append(ctx context.Context, msg Message)

    // 执行一个完整的推理-行动循环
    ThinkAct(ctx context.Context) (*Result, error)

    // 启动 Agent 主循环，直到 ctx 取消或收到 SigStop
    Run(ctx context.Context) error

    // 中断当前轮次但不结束 Run
    InterruptCurrentTurn() <-chan struct{}
}
```

### Agent 配置

```go
type Config struct {
    ID                string        // Agent 唯一标识符
    LLM               LLM           // （必需）推理后端
    System            System        // （必需）系统提示层
    Tools             Tools         // （必需）可用工具集
    AgentType         string        // Agent 类型标识（用于钩子事件）
    Color             string        // TUI 显示颜色
    CompactFunc       func(...)     // 对话压缩函数
    CWD               string        // 工作目录
    MaxTurns          int           // 每周期最大 LLM 推理轮数
    MaxOutputRecovery int           // 截断输出最大重试次数
    InboxBuf          int           // 入站缓冲区大小（默认 16）
    OutboxBuf         int           // 出站缓冲区大小（默认 64），-1 表示无出站（子 Agent 路径）
    OnEvent           func(Event)   // 同步事件观察者
}
```

`NewAgent(cfg)` 在 LLM、System 或 Tools 为 nil 时 panic —— 它们是必需的能力。

### Run 循环的三个阶段

```
Phase 1 — WAIT（阻塞）
  └─ 阻塞在 Inbox 上，等待消息到达
  └─ 收到 SigStop / ctx.Done → 触发 OnStop 钩子并返回

Phase 2 — DRAIN（非阻塞）
  └─ 排空 Inbox 中额外累积的消息
  └─ 所有消息追加到对话历史

Phase 3 — THINK + ACT（推理循环）
  └─ 循环：LLM.Infer → Tool.Execute → LLM.Infer → ...
  └─ 每轮之间非阻塞排空 Inbox
  └─ 发出流式块和工具结果到 Outbox
  └─ 循环直到 LLM 返回 end_turn
  └─ 回到 Phase 1
```

---

## 2. LLM（大语言模型推理后端）

**文件**：[`internal/core/llm.go`](../../internal/core/llm.go)

LLM 接口是无状态的流式推理后端：

```go
type LLM interface {
    Infer(ctx context.Context, req InferRequest) (<-chan Chunk, error)
}
```

### InferRequest（推理请求）

```go
type InferRequest struct {
    System   string       // 渲染后的系统提示
    Messages []Message    // 对话消息列表
    Tools    []ToolSchema // 可用工具 Schema
    Config   InferConfig  // 推理参数
}

type InferConfig struct {
    Model           string       // 模型名称
    MaxTokens       int          // 最大输出 Token
    Temperature     float64      // 温度参数
    ThinkingEffort  int          // 思考深度（Claude）
    ThinkingBudget  int          // 思考预算 Token
    ThinkingEnabled bool         // 是否启用思考模式
}
```

### InferResponse（推理响应）

```go
type InferResponse struct {
    Content        string       // 文本响应内容
    ToolCalls      []ToolCall   // 工具调用列表
    StopReason     StopReason   // 停止原因
    Usage          Usage        // Token 使用统计
    Thinking       string       // 思考过程（思考模式）
}

type Usage struct {
    InputTokens  int
    OutputTokens int
    CacheTokens  int
}

type StopReason string
const (
    EndTurn      StopReason = "end_turn"
    MaxTokens    StopReason = "max_tokens"
    ToolUse      StopReason = "tool_use"
    StopSequence StopReason = "stop_sequence"
    Refusal      StopReason = "refusal"
)
```

### Chunk（流式块）

```go
type Chunk struct {
    Content   string     // 文本增量
    Thinking  string     // 思考增量
    ToolCalls []ToolCall // 工具调用（仅在 Delta 模式）
    Done      bool       // 是否为最终块
    Usage     Usage      // 最终块的 Token 使用统计
}
```

---

## 3. Tool（工具）

**文件**：[`internal/core/tool.go`](../../internal/core/tool.go)

Tool 是 Agent 可以执行的单一能力。工具是**纯函数** —— 不知道钩子、权限或对话历史：

```go
type Tool interface {
    Name() string
    Description() string
    Schema() ToolSchema
    Execute(ctx context.Context, input map[string]any) (string, error)
}

type ToolSchema struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Parameters  any    `json:"input_schema,omitempty"` // JSON Schema 对象
}
```

### Tools（可变工具集合）

```go
type Tools interface {
    Get(name string) Tool
    All() []Tool
    Add(tool Tool, caller string)       // 注册或替换工具
    Remove(name, caller string)         // 移除工具
    Schemas() []ToolSchema
    SetObserver(fn func(ToolsChange))   // 设置变更观察者
}
```

Tools 可以在运行时动态变化 —— 钩子添加/移除工具、Agent 定义限制为只读、父 Agent 过滤子 Agent 的工具集。`Remove` 通过 `name` 取消注册，不存在的工具为 no-op。

---

## 4. System（系统提示词）

**文件**：[`internal/core/system.go`](../../internal/core/system.go)

System 管理可组合、可变的系统提示词。定义了 Agent 的**身份**、**知识**和**行为规范**：

```go
type System interface {
    Prompt() string                                      // 组装后的完整系统提示
    Use(sec Section, caller string)                      // 注册或替换一个片段
    Drop(name, caller string)                            // 移除一个片段
    Refresh(name, caller string)                         // 标记片段内容过期
    Sections() []Section                                 // 当前片段快照
    SetObserver(fn func(SystemChange))                   // 设置变更观察者
}
```

### Section（提示片段）

```go
type Section struct {
    Name     string // 稳定标识符（跨突变不变）
    Slot     int    // 渲染槽位
    Content  string // 渲染后的内容
    Caller   string // 触发变更的来源
    Renderer func() string // 惰性渲染函数
}
```

### Slot（槽位顺序）

系统提示词由多个 `Section` 组成，每个占用一个 `Slot`。槽位定义了渲染顺序，确保不同来源的提示片段有序组合。内置槽位包括：

- `Identity` — 核心身份定义
- `Procedures` — 操作程序
- `Tools` — 工具使用指南
- `Guardrails` — 安全护栏
- 等等

---

## 5. Message（消息）

**文件**：[`internal/core/message.go`](../../internal/core/message.go)

Message 是整个代码库使用的规范消息类型：

```go
type Message struct {
    ID                string         // 短十六进制标识符（8 字节随机数）
    Role              Role           // user / assistant / tool_result / notice
    Content           string         // 消息文本内容
    DisplayContent    string         // 显示用内容
    Images            []Image        // 图片附件
    Thinking          string         // 思考过程
    ThinkingSignature string         // 思考签名（Google Gemini）
    ToolCalls         []ToolCall     // 工具调用列表
    ToolResult        *ToolResult    // 工具执行结果
    From              string         // 来源标识
    Signal            Signal         // 控制信号（不入 JSON）
    Meta              map[string]any // 元数据
}
```

### 消息角色

```go
const (
    RoleUser      Role = "user"        // 用户消息
    RoleAssistant Role = "assistant"   // 助手消息
    RoleTool      Role = "tool_result" // 工具结果
    RoleNotice    Role = "notice"      // 通知（仅 UI）
)
```

### 控制信号

```go
const (
    SigStop    Signal = "stop"    // 优雅停止
    SigCompact Signal = "compact" // 对话压缩
)
```

### 关键构造函数

```go
func UserMessage(text string, images []Image) Message
func AssistantMessage(text, thinking string, calls []ToolCall) Message
func ErrorResult(tc ToolCall, content string) *ToolResult
func ToolResultMessage(result ToolResult) Message
```

---

## 6. Event（事件）

**文件**：[`internal/core/agent.go`](../../internal/core/agent.go)

Event 是 Agent 生命周期事件的载体，通过 Outbox Channel 发出：

```go
type Event struct {
    Type   EventType // 事件类型
    Source string    // 触发者（Agent ID、工具名、"user"）
    Data   any       // 载荷（类型取决于 EventType）
}
```

### 事件类型

| 事件 | 说明 | Data 类型 |
|------|------|-----------|
| `OnStart` | Agent 启动 | nil |
| `OnStop` | Agent 停止 | error 或 nil |
| `PreInfer` | LLM 调用前 | InferenceContext |
| `PostInfer` | LLM 响应后 | *InferResponse |
| `OnChunk` | 流式块 | Chunk |
| `PreTool` | 工具执行前 | ToolCall |
| `PostTool` | 工具执行后 | ToolResult |
| `OnMessage` | 收到入站消息 | Message |
| `OnAppend` | 消息追加到对话 | Message |
| `OnTurn` | 推理-行动周期完成 | Result |
| `OnCompact` | 对话压缩 | CompactInfo |
| `OnSystemChange` | 系统提示变更 | SystemChange |
| `OnToolsChange` | 工具注册表变更 | ToolsChange |

### 类型化的事件构造函数

```go
func StartEvent(agentID string) Event
func StopEvent(agentID string, err error) Event
func ChunkEvent(agentID string, c Chunk) Event
func TurnEvent(agentID string, r Result) Event
func PreInferEvent(agentID string, ctx InferenceContext) Event
func PostToolEvent(tr ToolResult) Event
// ... 等等
```

### 数据访问器

```go
func (e Event) ToolCall() (ToolCall, bool)
func (e Event) ToolResult() (ToolResult, bool)
func (e Event) Message() (Message, bool)
func (e Event) Result() (Result, bool)
func (e Event) Response() (*InferResponse, bool)
func (e Event) Chunk() (Chunk, bool)
func (e Event) Error() (error, bool)
```

---

## 7. Result（结果）

```go
type Result struct {
    Content    string     // 本轮最终文本输出
    Messages   []Message  // 完整对话历史
    Turns      int        // 本周期 LLM 推理轮数
    ToolUses   int        // 本周期工具调用次数
    TokensIn   int        // 消耗的输入 Token
    TokensOut  int        // 产生的输出 Token
    StopReason StopReason // 停止原因
    StopDetail string     // 人类可读的停止详情
}
```
