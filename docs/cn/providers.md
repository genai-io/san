# LLM 提供商详解

San 支持 11 个云端 LLM 提供商（另有 Ollama 本地提供商），通过空白导入（blank import）在 [`cmd/san/main.go`](../../cmd/san/main.go) 中自动注册。

---

## 提供商注册机制

### Provider 接口

定义在 [`internal/llm/types.go`](../../internal/llm/types.go)。每个提供商实现一个流式接口：

```go
type Provider interface {
    // 发起一次补全请求，返回流式块通道
    Stream(ctx context.Context, opts CompletionOptions) <-chan StreamChunk
    // 返回该提供商可用的模型列表（动态从 /models 拉取，而非硬编码目录）
    ListModels(ctx context.Context) ([]ModelInfo, error)
    // 提供商名称
    Name() string
}
```

### 可选的 ThinkingEffortProvider

暴露原生思考 / 推理力度的提供商额外实现此接口：

```go
type ThinkingEffortProvider interface {
    ThinkingEfforts(model string) []string      // 指定模型支持的思考级别
    DefaultThinkingEffort(model string) string  // 默认思考级别
}
```

### 自动注册

每个提供商实现包导出一个 `init()` 函数，在包被导入时自动注册：

```go
// 在 cmd/san/main.go 中
import (
    _ "github.com/genai-io/san/internal/llm/anthropic"
    _ "github.com/genai-io/san/internal/llm/openai"
    _ "github.com/genai-io/san/internal/llm/google"
    _ "github.com/genai-io/san/internal/llm/moonshot"
    _ "github.com/genai-io/san/internal/llm/alibaba"
    _ "github.com/genai-io/san/internal/llm/minmax"
    _ "github.com/genai-io/san/internal/llm/mimo"
    _ "github.com/genai-io/san/internal/llm/bigmodel"
    _ "github.com/genai-io/san/internal/llm/deepseek"
    _ "github.com/genai-io/san/internal/llm/ollama"
    _ "github.com/genai-io/san/internal/llm/sensenova"
    _ "github.com/genai-io/san/internal/llm/volcengine"
)
```

### 注册 API：`llm.Register`

每个提供商包在 `init()` 中调用 `llm.Register`，把一份元数据和一个工厂函数登记到包级注册表（[`internal/llm/store.go`](../../internal/llm/store.go)、[`registry.go`](../../internal/llm/registry.go)）：

```go
func Register(meta Meta, factory Factory)

type Meta struct {
    Provider    Name       // 提供商标识
    AuthMethod  AuthMethod // 认证方式（API Key、OAuth 等）
    EnvVars     []string   // 所需环境变量
    DisplayName string
}

type Factory func(ctx context.Context) (Provider, error)
```

例如 DeepSeek（[`internal/llm/deepseek/apikey.go`](../../internal/llm/deepseek/apikey.go)）：

```go
func init() {
    llm.Register(APIKeyMeta, NewAPIKeyClient)
}
```

---

## 各提供商详解

### 1. Anthropic（Claude）

- **包**：[`internal/llm/anthropic/`](../../internal/llm/anthropic/)
- **SDK**：`anthropics/anthropic-sdk-go`
- **API 变量**：`ANTHROPIC_API_KEY`
- **支持模型**：Claude Opus 4.8、Claude Sonnet 4.6、Claude Haiku 4.5
- **特性**：
  - 支持 **Thinking（思考模式）**，可配置思考预算
  - 支持 Prompt Caching 优化上下文重用
  - 支持 Vertex AI 路径
  - Tool Use 原生支持

**思考模式级别**：
```
off → low → medium → high → maximum
```

**配置示例**（`~/.san/providers.json`）：
```json
{
  "anthropic": {
    "api_key": "sk-ant-...",
    "model": "claude-sonnet-4-6"
  }
}
```

---

### 2. OpenAI（GPT、o-series、Codex）

- **包**：[`internal/llm/openai/`](../../internal/llm/openai/)
- **SDK**：`openai/openai-go/v3`
- **API 变量**：`OPENAI_API_KEY`
- **支持模型**：GPT-4o、GPT-4.1、o3、o4-mini、Codex
- **特性**：
  - Responses API 支持
  - Structured Output（JSON Schema）
  - Reasoning effort（o-series）

---

### 3. Google（Gemini）

- **包**：[`internal/llm/google/`](../../internal/llm/google/)
- **SDK**：`google.golang.org/genai`
- **API 变量**：`GOOGLE_API_KEY`
- **支持模型**：Gemini 2.5 Pro、Gemini 2.5 Flash
- **特性**：
  - 原生多模态支持（文本+图片+视频+音频）
  - Thought Signature（思考签名）用于 Gemini 思考模式
  - 超大上下文窗口（1M+ tokens）

---

### 4. Moonshot（Kimi）

- **包**：[`internal/llm/moonshot/`](../../internal/llm/moonshot/)
- **API 变量**：`MOONSHOT_API_KEY`
- **特性**：兼容 OpenAI API 格式

---

### 5. Alibaba（DashScope）

- **包**：[`internal/llm/alibaba/`](../../internal/llm/alibaba/)
- **API 变量**：`DASHSCOPE_API_KEY`
- **支持模型**：Qwen 系列、DeepSeek（通过 DashScope）
- **特性**：
  - Qwen 系列原生支持
  - 兼容 OpenAI API 格式
  - 提供 DeepSeek 模型访问

---

### 6. MiniMax

- **包**：[`internal/llm/minmax/`](../../internal/llm/minmax/)
- **API 变量**：`MINIMAX_API_KEY`

---

### 6.5 MiMo（小米）

- **包**：[`internal/llm/mimo/`](../../internal/llm/mimo/)
- **API 变量**：`MIMO_API_KEY`（可选 `MIMO_BASE_URL`，默认 `https://api.xiaomimimo.com/anthropic`）
- **支持模型**：MiMo V2.5 Pro、MiMo V2.5、MiMo V2 Pro、MiMo V2 Flash、MiMo V2 Omni
- **特性**：
  - 兼容 Anthropic API 格式（复用 anthropic provider 的流式逻辑）
  - 模型列表通过平台 API 获取，失败时回退到静态目录
  - 支持成本估算

---

### 7. Z.ai / BigModel（智谱 GLM）

- **包**：[`internal/llm/bigmodel/`](../../internal/llm/bigmodel/)
- **API 变量**：`BIGMODEL_API_KEY`
- **支持模型**：GLM 系列

---

### 8. DeepSeek

- **包**：[`internal/llm/deepseek/`](../../internal/llm/deepseek/)
- **API 变量**：`DEEPSEEK_API_KEY`（可选 `DEEPSEEK_BASE_URL`，默认 `https://api.deepseek.com`）
- **特性**：兼容 OpenAI API 格式（复用 openai-go SDK），支持 `reasoning_effort` 推理力度
- **支持模型**：DeepSeek V4 Flash、DeepSeek V4 Pro

---

### 9. Ollama（本地模型）

- **包**：[`internal/llm/ollama/`](../../internal/llm/ollama/)
- **API 变量**：`OLLAMA_BASE_URL`（默认 `http://localhost:11434/v1`，无需 API Key）
- **特性**：
  - 本地运行开源模型，数据不出本机
  - 兼容 OpenAI API 格式
  - 支持任何 Ollama 兼容模型（Llama、Mistral、Qwen 等）

---

### 10. SenseNova（商汤日日新）

- **包**：[`internal/llm/sensenova/`](../../internal/llm/sensenova/)
- **API 变量**：`SENSENOVA_API_KEY`（可选 `SENSENOVA_BASE_URL`）
- **特性**：
  - 兼容 Anthropic Messages API 格式
  - 使用静态模型目录，无需动态拉取模型列表
  - 支持 Bearer Token 认证

---

### 11. Volcengine Ark（火山引擎方舟）

- **包**：[`internal/llm/volcengine/`](../../internal/llm/volcengine/)
- **API 变量**：`VOLCENGINE_API_KEY`（可选 `VOLCENGINE_BASE_URL`，默认 `https://ark.cn-beijing.volces.com/api/coding`；可选 `VOLCENGINE_MODEL` 作为模型列表回退）
- **特性**：
  - 兼容 Anthropic Messages API 格式（复用 anthropic provider 的流式逻辑）
  - 优先从兼容的 `/v1/models` 获取模型列表，失败时回退到 `VOLCENGINE_MODEL`
  - 支持 Bearer Token 认证

---

## LLM 接口实现

所有提供商都通过 `core.LLM` 接口暴露，保证统一的调用方式：

```go
type LLM interface {
    Infer(ctx context.Context, req InferRequest) (<-chan Chunk, error)
    InputLimit() int   // 该模型的输入上下文上限
}
```

### Provider → core.LLM 的适配

提供商包实现的是上面的 `llm.Provider`（`Stream` 流式接口）。`internal/llm` 中的 `Client` 把"某个 `Provider` + 具体模型"适配为 Agent 实际使用的 `core.LLM`：统计每次调用的 Token、流式产出 `core.Chunk`、并应用重试与成本逻辑。其转换步骤：
1. 将 `InferRequest.System` + `InferRequest.Messages` 转换为提供商的消息格式
2. 将 `InferRequest.Tools` 转换为提供商的工具定义格式
3. 调用提供商 SDK 发起流式请求
4. 将提供商 SDK 的流式响应转换为 `<-chan core.Chunk`

### Conn

`Conn` 是一个**结构体**，是当前活动 LLM 的句柄：持有已连接的提供商、当前模型与提供商/模型 Store，全部由同一把互斥锁保护，并按需创建客户端（[`internal/llm/service.go`](../../internal/llm/service.go)）：

```go
type Conn struct { /* 内部字段不导出，每个访问器都加锁 */ }

func (c *Conn) Provider() Provider
func (c *Conn) SetCurrentModel(info *CurrentModelInfo)
func (c *Conn) NewClient(model string, maxTokens int) *Client  // 为一次推理创建客户端
func (c *Conn) Store() *Store
```

每次 Agent 启动时通过 `NewClient(model, maxTokens)` 创建新的 `*Client`（因模型切换、会话恢复等原因）。

### 成本追踪

实现在 [`internal/llm/money.go`](../../internal/llm/money.go)：
- 每个模型有每百万 Token 的输入/输出价格
- 从 API 响应中提取 Token 使用量
- 计算并累积会话成本

### 日志记录

实现在 [`internal/llm/logging.go`](../../internal/llm/logging.go)：
- 记录每次推理请求和响应
- 包含模型、Token 使用量、错误信息
- 用于调试和审计

---

## 搜索后端

除了 LLM 提供商，San 也支持可插拔的搜索后端（用于 WebSearch 工具）：

| 后端 | API 变量 | 说明 |
|------|----------|------|
| **Exa** | 无需密钥 | 默认后端 |
| **Tavily** | `TAVILY_API_KEY` | |
| **Brave** | `BRAVE_API_KEY` | |
| **Serper** | `SERPER_API_KEY` | |

通过 `/search` 斜杠命令切换。实现在 [`internal/search/`](../../internal/search/)。

---

## 模型切换

用户通过 `/model` 斜杠命令切换模型：

```
/model → 显示提供商列表 → 选择提供商 → 选择模型 → 更新 providers.json
```

切换模型后：
1. 新的 LLM 客户端使用新模型创建
2. 现有 Agent 会话不受影响（保留历史消息）
3. 下次推理使用新模型
