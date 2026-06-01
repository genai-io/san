# LLM 提供商详解

Gen Code 支持 8 个 LLM 提供商，通过空白导入（blank import）在 [`cmd/gen/main.go`](../../cmd/gen/main.go) 中自动注册。

---

## 提供商注册机制

### Provider 接口

定义在 [`internal/llm/types.go`](../../internal/llm/types.go)：

```go
type Provider interface {
    Name() string
    Models() []Model
    NewClient(cfg Config) (core.LLM, error)
}
```

### 可选的 ThinkingEffortProvider

```go
type ThinkingEffortProvider interface {
    Provider
    ThinkingEfforts() []ThinkingEffort  // 返回支持的思考级别
}
```

### 自动注册

每个提供商实现包导出一个 `init()` 函数，在包被导入时自动注册：

```go
// 在 cmd/gen/main.go 中
import (
    _ "github.com/genai-io/gen-code/internal/llm/anthropic"
    _ "github.com/genai-io/gen-code/internal/llm/openai"
    _ "github.com/genai-io/gen-code/internal/llm/google"
    _ "github.com/genai-io/gen-code/internal/llm/moonshot"
    _ "github.com/genai-io/gen-code/internal/llm/alibaba"
    _ "github.com/genai-io/gen-code/internal/llm/minmax"
    _ "github.com/genai-io/gen-code/internal/llm/bigmodel"
    _ "github.com/genai-io/gen-code/internal/llm/deepseek"
)
```

### ProviderStore

全局提供商注册表：

```go
type ProviderStore interface {
    Register(p Provider)
    Get(name string) (Provider, bool)
    List() []Provider
    Default() Provider
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

**配置示例**（`~/.gen/providers.json`）：
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

### 7. Z.ai / BigModel（智谱 GLM）

- **包**：[`internal/llm/bigmodel/`](../../internal/llm/bigmodel/)
- **API 变量**：`BIGMODEL_API_KEY`
- **支持模型**：GLM 系列

---

### 8. DeepSeek

- **包**：[`internal/llm/deepseek/`](../../internal/llm/deepseek/)
- **API 变量**：`DASHSCOPE_API_KEY`（通过 Alibaba）或自有密钥
- **特性**：兼容 OpenAI API 格式
- **支持模型**：DeepSeek V3、DeepSeek R1

---

## LLM 接口实现

所有提供商都通过 `core.LLM` 接口暴露，保证统一的调用方式：

```go
type LLM interface {
    Infer(ctx context.Context, req InferRequest) (<-chan Chunk, error)
}
```

### InferRequest 到提供商 API 的转换

每个提供商的 `NewClient` 返回一个实现 `core.LLM` 的结构体。其 `Infer` 方法：
1. 将 `InferRequest.System` + `InferRequest.Messages` 转换为提供商的消息格式
2. 将 `InferRequest.Tools` 转换为提供商的工具定义格式
3. 调用提供商 SDK 发起流式请求
4. 将提供商 SDK 的流式响应转换为 `<-chan Chunk`

### ClientFactory

```go
type ClientFactory func(cfg Config) (core.LLM, error)
```

`ClientFactory` 是创建 LLM 客户端的工厂函数。每次 Agent 启动时创建新的 LLM 客户端（因模型切换、对话框恢复等原因）。

### 成本追踪

实现在 [`internal/llm/cost.go`](../../internal/llm/cost.go)：
- 每个模型有每百万 Token 的输入/输出价格
- 从 API 响应中提取 Token 使用量
- 计算并累积会话成本

### 日志记录

实现在 [`internal/llm/log.go`](../../internal/llm/log.go)：
- 记录每次推理请求和响应
- 包含模型、Token 使用量、错误信息
- 用于调试和审计

---

## 搜索后端

除了 LLM 提供商，Gen Code 也支持可插拔的搜索后端（用于 WebSearch 工具）：

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
```
