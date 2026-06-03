# Gen Code 代码详解（中文文档）

## 项目概述

**Gen Code** 是一个开源的终端 AI 编程助手，使用 Go 语言编写。它是 [Claude Code](https://claude.ai/code) 的 Go 重写版本，编译为单一二进制文件，跨平台运行。项目定位是更快、更轻量的 Claude Code 替代品：下载体积缩小 5 倍，启动速度快 20 倍，内存占用减少 6 倍，实际任务执行快 4-8 倍。

### 核心特性

- **多 LLM 提供商**：支持 Anthropic Claude、OpenAI GPT/o-series、Google Gemini、Moonshot/Kimi、阿里云 DashScope（Qwen/DeepSeek）、MiniMax、Z.ai/智谱 GLM、DeepSeek
- **可插拔搜索后端**：Exa、Tavily、Brave、Serper
- **可定制身份/人格**：通过 Markdown 定义的自定义系统提示词
- **六大扩展机制**：Skills、Plugins、MCP Server、Hooks、Slash Commands、Subagents
- **会话持久化**：自动保存、恢复、分叉、上下文自动压缩
- **事件驱动架构**：基于 Bubble Tea TUI 框架的 MVU 循环
- **发布/订阅事件总线**：支持并行子 Agent 执行

### 性能对比

与 Claude Code v2.1.112 对比（Apple Silicon, 同模型 `claude-sonnet-4-6`）：

| 指标 | Gen Code | Claude Code | 优势 |
|------|---------|-------------|------|
| 下载体积 | 12 MB | 63 MB (+ Node.js 112 MB) | **5x 更小** |
| 磁盘占用 | 38 MB | 175 MB | **4.6x 更小** |
| 启动时间 | ~0.01s | ~0.20s | **20x 更快** |
| 启动内存 | ~32 MB | ~189 MB | **5.8x 更少** |
| 简单任务 | ~2.4s / 39 MB | ~10.4s / 286 MB | **4.3x 更快** |
| 工具调用任务 | ~3.3s / 39 MB | ~26.0s / 285 MB | **7.9x 更快** |

---

## 文档导航

| 文档 | 内容 |
|------|------|
| [architecture.md](architecture.md) | 架构总览：五层模型、运行时模型、设计原则 |
| [core-interfaces.md](core-interfaces.md) | 核心接口详解：Agent、LLM、Tool、System、Message、Event |
| [tools.md](tools.md) | 工具系统：内置工具（约 21 个）的 Schema 与实现 |
| [extensions.md](extensions.md) | 扩展模型：Skills、Plugins、MCP、Hooks、Commands、Subagents |
| [data-flow.md](data-flow.md) | 数据流详解：输入→Agent→渲染的完整链路 |
| [providers.md](providers.md) | LLM 提供商：注册机制、接口、各提供商特性 |
| [packages.md](packages.md) | 包结构详解：28 个核心包的职责与依赖关系 |
| [inspector.md](inspector.md) | Inspector：会话转录查看器（`gen inspector`）|

## 技术栈

- **语言**：Go 1.25.6
- **CLI 框架**：[Cobra](https://github.com/spf13/cobra)（命令行解析）
- **TUI 框架**：[Bubble Tea](https://github.com/charmbracelet/bubbletea)（MVU 终端 UI）
- **Markdown 渲染**：[Glamour](https://github.com/charmbracelet/glamour)
- **终端样式**：[Lip Gloss](https://github.com/charmbracelet/lipgloss)
- **日志**：[Zap](https://github.com/uber-go/zap)（结构化日志）+ Lumberjack（日志轮转）
- **LLM SDK**：Anthropic SDK、OpenAI SDK、Google GenAI SDK
- **Shell 解析**：[mvdan/sh](https://github.com/mvdan/sh)
- **文本 Diff**：[gotextdiff](https://github.com/hexops/gotextdiff)
- **Glob 匹配**：[doublestar](https://github.com/bmatcuk/doublestar)

## 快速开始

```bash
# 安装
curl -fsSL https://raw.githubusercontent.com/genai-io/gen-code/main/install.sh | bash

# 构建
git clone https://github.com/genai-io/gen-code.git
cd gen-code
make build

# 运行
gen                            # 交互模式
gen "解释一下这个函数"           # 带初始提示的交互模式
gen -p "你的问题"               # 非交互打印模式
gen -c                         # 恢复最近的会话
gen -r                         # 选择并恢复历史会话
```

## 许可证

Apache License 2.0
