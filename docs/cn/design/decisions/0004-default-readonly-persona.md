# ADR-0004: 默认只读 Persona

## 状态

提案中 — 2026-06-17.

## 背景

San 目前没有默认 Persona。当没有明确选择 Persona 时，系统提示词由内置默认部分拼装，
所有工具均可用 —— 包括文件编辑、Git 提交、Shell 命令等写操作。

这意味着一个全新的 San 会话没有任何只读保护。常见的初始工作流 ——
"解释这段代码"、"这个测试为什么失败"、"这个包是做什么的" —— 本不需要写权限，
但用户却得到了全部权限。

目标是提供一个只读 Persona，具有两个核心优势：

1. **环境保护**：Persona 在物理上无法修改用户的工作环境 ——
   不能写文件、不能变更 Git 状态、不能安装包。这消除了意外或幻觉产生的破坏性操作
   损坏项目状态的风险。

2. **节省 Token**：默认系统提示词携带了大量与只读工作无关的内容 ——
   工程方法论、提交规范、Git 协议、任务管理规则、针对破坏性操作的安全约束。
   只读 Persona 用最简化的、针对性的 prompt 替换全部三个散文部分，
   删除所有不服务于阅读、分析和解释的文本。

## 核心模型

一个**"readonly" Persona**，以标准 Persona 目录形式分发。与早期草案中回退到
San 默认 `behavior.md` 不同，此 Persona **覆盖全部三个散文部分**，
每个部分都采用最简化、针对只读场景的内容。San 默认的工程提示词中
没有任何内容残留 —— 模型只看到只读助手所需的内容。

```
.san/personas/readonly/
├── system/
│   ├── identity.md       ← 最简："你是一个只读助手"
│   ├── behavior.md       ← 最简：如何分析、解释、调试
│   └── rules.md          ← 最简：只读约束 + 被要求写操作时的回应方式
└── settings.json         ← permissions.deny 阻止写工具（强制执行层）
```

该 Persona 是普通的 Persona 目录 —— 无需任何引擎改动。它发布在独立仓库
[github.com/genai-io/readonly-persona](https://github.com/genai-io/readonly-persona)，
用户通过 `git clone` 安装到 `~/.san/personas/readonly/`。主仓库可以包含
轻量引用或示例来展示模式并链接到独立仓库，但不负责维护完整的 Persona。

每个部分都刻意精简。设计原则：**如果一句话无助于模型阅读、分析或解释，就删掉。**

### Prompt 内容

`system/identity.md`：

```markdown
你是一个只读助手。你回答问题、分析代码、调试环境。
你绝不修改文件、代码、配置或系统状态。
```

`system/behavior.md`：

```markdown
## 分析

分析代码时：先通读相关文件，系统性地追踪逻辑，在给出修复建议前找准根因。
解释时：清晰简洁。使用具体的引用（文件路径、行号、函数名）。

## 调试

调试时：检查日志、检查状态、追踪执行路径。
运行只读诊断命令（ls、cat、grep、git log、git status、git diff）。
先定位问题，再解释问题。

## 沟通

- 回答所问的问题 —— 不跑题。
- 直接回答优于铺陈叙述。
- 如需更多上下文，追问而非猜测。
```

`system/rules.md`：

```markdown
## 只读约束

你当前处于只读模式。以下操作被禁止：

- 创建、修改或删除文件
- 执行会写入文件系统的 Shell 命令
- 改变仓库状态的 Git 操作（commit、push、merge、rebase、tag 等）
- 安装包、依赖或系统软件
- 修改任何配置

## 你可以做什么

- 读取文件和搜索代码库
- 回答关于代码、架构、设计和规范的问题
- 分析 Bug、追踪执行路径、解释行为
- 运行只读 Shell 命令：ls、cat、grep、find、git log、git status、
  git diff、git show、git blame 等
- 调试环境和诊断问题

## 被要求写操作时

说明你处于只读模式，无法执行写操作。
建议通过 `/persona <name>` 切换到有写权限的 Persona。
```

### 为什么覆盖 behavior.md

San 的默认 `behavior.md` 包含了约 30 行工程方法论：工作习惯（先读设计文档、
遵循分层架构、编写单元测试、运行 lint）、沟通风格（报告改了哪些文件、关联 PR）、
范围控制。这些对只读会话完全无用。用最简化的替代内容覆盖它可以回收这些 Token，
并给模型提供真正与阅读分析相关的指导。

### 为什么覆盖 rules.md

San 的默认 `rules.md` 集成了安全策略、工具协议、任务/Git 规范、破坏性命令的安全规则、
以及各 Provider 的特殊行为。对只读会话而言这些都是无效负载 ——
Persona 根本没有写能力，关于提交信息格式、PR 规范、`--no-verify` 的规则纯属噪音。
最简化的替代内容仅保留只读约束。

### Token 节省估算

San 内置默认值与只读替代内容的行数对比：

| 部分 | 默认（约行数） | 只读（约行数） | 节省 |
|---|---|---|---|
| `identity` | ~3 | ~2 | 较少 |
| `behavior` | ~30 | ~12 | ~60% |
| `rules` | ~120 | ~18 | ~85% |
| **合计** | **~153** | **~32** | **~80%** |

这些节省对每轮包含系统提示词的推理都生效（包括缓存未命中时）。
同时，因为 Persona 提示词属于 prompt-cache 前缀的一部分，更小的提示词也意味着更小的缓存条目。

### 允许的只读操作

| 类别 | 示例 |
|---|---|
| 读取文件 | `Read`、`cat`、`head`、`tail` |
| 搜索 | `grep`、`find`、`git grep` |
| Git 读取 | `git log`、`git status`、`git diff`、`git show`、`git blame` |
| 分析 | 代码审查、架构分析、Bug 追踪 |
| 问答 | 关于代码、设计、规范的问题 |
| 调试 | 追踪错误、检查日志、检查环境状态 |

### 禁止的写操作

| 类别 | 示例 |
|---|---|
| 文件写入 | `Edit`、`Write`、Shell 重定向（`>`、`>>`）、`tee` |
| 文件删除 | `rm`、`rmdir`、`shred` |
| 文件移动/复制 | `mv`、`cp`、`dd` |
| 文件创建 | `touch`、`mkdir` |
| 权限修改 | `chmod`、`chown` |
| Git 写入 | `commit`、`push`、`merge`、`rebase`、`tag`、`am`、`cherry-pick`、`stash` |
| 包安装 | `go install`、`npm install`、`pip install`、`make install`、`brew install` |
| 破坏性操作 | `rm -rf`、force push、`git reset --hard` |

## 决策

### D1：以 Persona 目录形式分发，而非内置于二进制文件

Readonly Persona 是标准的 Persona 目录 —— 与任何用户创建的 Persona 格式完全一致。
无需引擎改动、无需 `embed.FS`、无需特殊加载路径。现有 Persona 系统已支持其全部需求：
`system/{identity,behavior,rules}.md` 加上一个包含 `permissions.deny` 的 `settings.json`。

它发布在 [github.com/genai-io/readonly-persona](https://github.com/genai-io/readonly-persona)。
用户通过 `git clone` 安装到 `~/.san/personas/readonly/` 或
`.san/personas/readonly/`。安装后 `/persona readonly` 即可正常切换，
并在 `/persona` 选择器中可见。

San 主仓库可以包含轻量引用（README 指针或最小示例）来展示模式及链接到独立仓库，
但不负责维护完整的 Persona。

### D2：暂不作为系统默认值

Readonly Persona **默认可用**，但未选择 Persona 时的系统默认行为暂时不变 ——
保持全权限访问。

用户通过 `/persona readonly` 或在设置中配置 `persona: readonly` 来主动选择。
是否将其设为系统默认值，留待评估迁移影响后再决定。

**理由**：改变默认 Persona 会打破用户对 San 会话启动即有全工具访问的预期。
这需要充分的社区沟通，而不仅仅是代码变更。先交付 Persona 本身，待社区达成共识后再调整默认值。

### D3：覆盖全部三个散文部分 —— 不回退到默认值

Persona 提供自己的 `identity.md`、`behavior.md` 和 `rules.md`。
不使用任何 San 的默认散文部分。这是有意为之：

- **Token 效率**：默认部分承载了大量工程工作流的内容。去掉它们可节省系统提示词中
  约 80% 的散文（见上文估算）。
- **信号清晰**：模型只收到与只读任务相关的指令。没有工程方法论、Git 协议或提交规范
  稀释提示词。
- **无死规则**：关于 `--no-verify`、提交信息格式、分支命名、PR 描述的规则
  在 Persona 无法写入的情况下纯属噪音。

每个部分保持最简 —— 设计目标是能产生正确只读行为的最短提示词。

### D4：双层防御 —— permissions.deny（强制）+ rules.md（建议）

与 Persona 模型的设计哲学一致，采用双层防御：

1. **`settings.json` 的 `permissions.deny`** —— *强制执行*层。在权限引擎层
   阻止写工具调用，模型根本收不到这些工具。根据 Persona 权限合并语义，
   该 deny 列表不能被更低层（用户/项目设置）放松。

2. **`system/rules.md`** —— *建议*层。模型每轮阅读的自然语言约束。
   即使某个工具漏过了 deny 列表，模型也被引导远离写操作。

```json
{
  "description": "只读 Persona — 回答问题、分析代码、调试环境。不可写入。",
  "skills": {},
  "agents": [],
  "disabledTools": {},
  "permissions": {
    "defaultMode": "default",
    "deny": [
      "Edit",
      "Write",
      "Bash(rm:*)",
      "Bash(rmdir:*)",
      "Bash(mv:*)",
      "Bash(cp:*)",
      "Bash(touch:*)",
      "Bash(mkdir:*)",
      "Bash(dd:*)",
      "Bash(shred:*)",
      "Bash(chmod:*)",
      "Bash(chown:*)",
      "Bash(git commit:*)",
      "Bash(git push:*)",
      "Bash(git merge:*)",
      "Bash(git tag:*)",
      "Bash(git rebase:*)",
      "Bash(git reset:*)",
      "Bash(git am:*)",
      "Bash(git cherry-pick:*)",
      "Bash(git stash:*)",
      "Bash(go install:*)",
      "Bash(npm install:*)",
      "Bash(yarn add:*)",
      "Bash(pip install:*)",
      "Bash(pip3 install:*)",
      "Bash(brew install:*)",
      "Bash(make install:*)",
      "Bash(curl * | *)",
      "Bash(wget -O:*)"
    ]
  }
}
```

### D5：Git hooks —— 已评估，暂缓实施

Git hooks 曾被考虑作为第三层防御，但决定暂缓：

- **优点**：pre-commit hook 可以在 Git 层面阻止提交，独立于 San 的工具权限体系，
  实现纵深防御。
- **缺点**：hooks 仅覆盖 Git 操作，不覆盖一般文件写入。权限引擎已经覆盖了全部工具面。
  hooks 需要按仓库单独安装。活跃 Persona 是 San 运行时概念 —— hooks 没有原生的查询方式。

**后续如需**：提供一个 `san persona current` CLI 命令来输出当前活跃 Persona。
pre-commit hook 可以调用它：

```bash
#!/bin/bash
if [ "$(san persona current 2>/dev/null)" = "readonly" ]; then
  echo "只读 Persona 模式下禁止提交。"
  exit 1
fi
```

这属于未来扩展，不在本 ADR 实施范围内。

### D6：子 Agent 继承

当 Readonly Persona 通过 Agent 工具创建子 Agent 时，只读约束通过权限层传播 ——
子 Agent 使用相同的 effective settings overlay。无需特殊的子 Agent 处理逻辑。

## 实现计划

### 阶段一：Persona 目录

创建 Persona 目录：

```
.san/personas/readonly/
├── system/
│   ├── identity.md
│   ├── behavior.md
│   └── rules.md
└── settings.json
```

按上文 [Prompt 内容](#prompt-内容) 编写三个 prompt 文件（`identity.md`、`behavior.md`、
`rules.md`），以及包含 [D4](#d4双层防御--permissionsdeny强制--rulesmd建议) 中 deny 列表的 `settings.json`。

### 阶段二：发布为独立仓库

已发布在 [github.com/genai-io/readonly-persona](https://github.com/genai-io/readonly-persona)。
用户通过以下命令安装：

```bash
git clone https://github.com/genai-io/readonly-persona.git ~/.san/personas/readonly
```

### 阶段三：主仓库引用（可选）

可选择在 San 主仓库中添加指针或最小示例，以展示 Persona 模式并链接到独立仓库 ——
不将完整 Persona 的维护责任纳入核心。

## 影响

### 正面

- **环境保护**：工作目录免受意外修改。无文件损坏、无意外的 Git 变更、
  无幻觉导致的破坏性命令。
- **Token 节省（约 80%）**：Persona 将约 153 行默认系统提示词散文替换为约 32 行
  只读专用内容。更小的提示词在每轮推理中节省 Token，同时减小 prompt-cache 体积。
- **信号清晰**：模型只收到与阅读、分析和解释相关的指令 ——
  没有工程方法论或 Git 协议稀释提示词。
- **安全的起点**：用户可以安全地探索、提问和分析代码，无需担心意外写入。
- **显式提权**：写操作需要主动切换 Persona，使用户意图明确清晰。
- **无需新增基础设施**：复用现有 Persona 系统、权限引擎和 settings overlay，
  零新增机制。

### 负面 / 代价

- **Deny 列表覆盖度**：deny 列表必须显式枚举所有写工具和命令模式。
  未来添加的新的可写工具（或不符合 deny 模式的创造性 Shell 命令）可能漏过。
  建议层的 `rules.md` 可以缓解但不能根除这个缺陷。
- **非沙箱**：San Persona 运行在用户的信任级别上 —— 这是护栏，不是安全边界。
  有决心的用户或插件可以绕过。
- **用户困惑**：期望开箱就能编辑文件的用户切换到 readonly 模式后会被阻止。
  Persona 选择器必须清晰展示每个 Persona 的能力边界。

## 参考资料

- [`persona.md`](../../concepts/persona.md) — Persona 系统设计
- [`permission-model.md`](../../concepts/permission-model.md) — 权限引擎
- [`ADR-0001`](0001-layered-package-architecture.md) — 分层包架构
- [`ADR-0002`](0002-autonomous-dev-management.md) — 自主开发团队（Persona 配置示例）
