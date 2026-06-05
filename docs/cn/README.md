# San 代码详解（中文文档）

本目录是 **San** 的中文「代码详解」文档集，面向贡献者与想深入源码的读者，逐层讲解架构、核心接口、工具系统与扩展机制。

> 产品概述、安装、用法与性能对比请见根目录的 [README.zh.md](../../README.zh.md)。

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
| [inspector.md](inspector.md) | Inspector：会话转录查看器（`san inspector`）|

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
curl -fsSL https://raw.githubusercontent.com/genai-io/san/main/install.sh | bash

# 构建
git clone https://github.com/genai-io/san.git
cd san
make build

# 运行
san                            # 交互模式
san "解释一下这个函数"           # 带初始提示的交互模式
san -p "你的问题"               # 非交互打印模式
san -c                         # 恢复最近的会话
san -r                         # 选择并恢复历史会话
```

## 许可证

Apache License 2.0
