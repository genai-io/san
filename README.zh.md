<div align="center">
  <h1>&lt; SAN ✦ /&gt;</h1>
  <p><strong>开销最小，Agent 最强。</strong></p>
  <p>上下文精简，原生性能，从里到外都开放。</p>
  <p>
    <a href="https://github.com/genai-io/san/releases"><img src="https://img.shields.io/github/v/release/genai-io/san?style=flat-square" alt="Release"></a>
    <a href="https://genai-io.github.io/san/"><img src="https://img.shields.io/badge/%E5%AE%98%E7%BD%91-0d9488?style=flat-square" alt="官网"></a>
    <a href="https://genai-io.github.io/san/getting-started.html"><img src="https://img.shields.io/badge/%E5%BF%AB%E9%80%9F%E5%BC%80%E5%A7%8B-0d9488?style=flat-square" alt="快速开始"></a>
    <a href="docs/index.md"><img src="https://img.shields.io/badge/%E6%96%87%E6%A1%A3-0d9488?style=flat-square" alt="文档"></a>
    <a href="https://www.producthunt.com/products/san?launch=san"><img src="https://img.shields.io/badge/Product%20Hunt-da552f?style=flat-square&logo=producthunt&logoColor=white" alt="Product Hunt"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?style=flat-square" alt="License"></a>
  </p>
  <p>
    <a href="README.md">English</a> · <strong>简体中文</strong>
  </p>
  <p>
    <a href="https://genai-io.github.io/san/intro.html?variant=readme"><picture>
      <source media="(prefers-color-scheme: dark)" srcset="assets/san-intro-dark.gif">
      <img src="assets/san-intro.gif" alt="San 动态简介" width="100%">
    </picture></a>
  </p>
  <sub><a href="https://genai-io.github.io/san/intro.html?variant=readme">打开高清完整版 ↗</a></sub>
</div>

## 为什么选 San

- **小而快** —— 单个原生 Go 二进制，保持精简的上下文开销，无需 Node.js 或 Python。
- **更少预设的 Agent 框架** —— 不绑定模型厂商，通过统一接口，使用 system prompt、persona、skills 和工具塑造 Agent 行为。
- **多 Agent 协作** —— 子 Agent 协作、跨会话消息通信与动态工作流。
- **控制与可观测性** —— 配置执行权限，检查模型上下文与工具调用。

[功能](docs/features.md) · [架构](docs/concepts/architecture.md)

<sub>*关于名字 —— **San**，即 **三**，符号取自 **☰**。语出《道德经》「三生万物」—— 一个运行时即可化身为任意 Agent，并以三步循环运转（推理 → 行动 → 观察）。命令仍是 `san`。*</sub>

## 安装

**Homebrew (macOS / Linux)**

```bash
brew install genai-io/san/san
```

**macOS / Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/genai-io/san/main/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/genai-io/san/main/install.ps1 | iex
```

<details>
<summary><b>其他安装方式与卸载</b></summary>

**卸载**

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/genai-io/san/main/install.sh | bash -s uninstall
```

```powershell
# Windows (PowerShell)
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/genai-io/san/main/install.ps1))) uninstall
```

**通过 Go Install（需要 Go 1.26.9+）**

```bash
go install github.com/genai-io/san/cmd/san@latest
```

**从源码构建**

```bash
git clone https://github.com/genai-io/san.git
cd san
go build -o san ./cmd/san
mkdir -p ~/.local/bin && mv san ~/.local/bin/
```

</details>

<details>
<summary><b>更新</b></summary>

- **自动更新：** 后台安装新版本，状态栏提示重启。
- **手动更新：** 运行 `san update`；Homebrew 安装使用 `brew update && brew upgrade genai-io/san/san`。Homebrew 配方每天同步，新版本发布后可能稍有延迟。
- **关闭自动更新：** 在 `/settings` › General 中设置，或设置 `SAN_DISABLE_AUTOUPDATE=1`。

</details>

## 使用

运行 `san`，按提示选择模型并添加 API key。

```bash
san                          # 交互模式
san "解释这个函数"            # 一次性运行
san -p "做某件事"             # print 模式（无 TUI），可管道
san --continue               # 恢复最近的会话
san --resume                 # 选择历史会话恢复
```

使用 `san --help` 查看命令行选项，使用 `/help` 查看交互命令。

## 基准测试：San vs Claude Code

在 Apple Silicon 上对比 [Claude Code](https://claude.ai/code) v2.1.112，使用相同模型（`claude-sonnet-4-6`）：

| 指标 | San | Claude Code | 优势 |
|--------|---------|-------------|-----------|
| 下载大小 | 12 MB | 63 MB（+ Node.js 112 MB） | **小 5 倍** |
| 磁盘占用 | 38 MB | 175 MB | **小 4.6 倍** |
| 启动耗时 | ~0.01s | ~0.20s | **快 20 倍** |
| 启动内存 | ~32 MB | ~189 MB | **省 5.8 倍** |
| 简单任务 | ~2.4s / 39 MB | ~10.4s / 286 MB | **快 4.3 倍、省内存 7.3 倍** |
| 工具调用任务 | ~3.3s / 39 MB | ~26.0s / 285 MB | **快 7.9 倍、省内存 7.2 倍** |
| 框架上下文开销* | ~2.3k token | ~20.9k token | **省约 9 倍** |

<sub>*上下文开销 = 空回合下的 system prompt + 工具 schema，单独在 San v1.22.0 与 Claude Code v2.1.220 上测量，[方法见此](docs/operations/benchmark.md#7-context-overhead-first-turn)；其余各行来自 v1.13.2 / v2.1.112 那次测试。</sub>

特性大体可比 —— 差距来自客户端开销，不是能力缺斤少两。[`docs/operations/benchmark.md`](docs/operations/benchmark.md)

## 文档

- [开始使用](docs/index.md#get-started) —— 安装、配置、命令与权限。
- [功能](docs/features.md) —— 功能清单与详细文档。
- [定制与扩展](docs/index.md#customize-and-extend) —— 提示词、persona、技能与集成。
- [开发与贡献](docs/index.md#develop-and-contribute) —— 架构、开发流程与设计。

## 相关项目

- [Claude Code](https://claude.ai/code) —— Anthropic 的 AI 编程助手
- [Aider](https://github.com/Aider-AI/aider) —— 终端中的 AI 结对编程
- [Continue](https://github.com/continuedev/continue) —— 开源 AI 编程助手

## 社区

两个入口 —— 国内用微信，海外用 Slack，欢迎入群一起讨论：

- **微信** — 关注公众号「极客外传」· 回复 `san` 或 `三` 入群
- **Slack** — [点击加入 Slack](https://join.slack.com/t/sanaico/shared_invite/zt-3zvfr8v6f-dchFpvpufY7fKA7tG7lhIg)

## 贡献

欢迎贡献！请阅读 [CONTRIBUTING.md](CONTRIBUTING.md) 中的指南。

## 许可证

Apache License 2.0 —— 详见 [LICENSE](LICENSE)。
