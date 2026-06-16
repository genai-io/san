# ADR-0002: 自主开发管理团队

## 状态

提案中 — 2026-06-11. 更新于 2026-06-16.

## 背景

当前 San 项目（`genai-io/san`）的开发依赖人来驱动 Agent：
人在每个会话中手动控制 Agent，不能自动管理整个项目的 issues、feature 和 bug。

目标是把 San 项目变成一个**由 AI 自主管理的项目**：
人只需要告诉系统"要实现什么功能"或"修复所有 Bug"，
剩下的由一组 Persona 自动协同完成 —— 拆解需求、设计架构、
编码实现、验证测试、最终发布。人不再写代码，只提需求。

## 核心模型

在 Org（`genai-io`）下新建一个独立的 Repo
`san-team`，专门存放 San 开发团队的 Persona。
每个团队是一组为特定项目协同工作的 Persona。

每个 Persona 就是一个 Persona 目录，
格式遵循 [`persona-system.md`](../../notes/active/persona-system.md) 的设计规范。

```
genai-io（Org，已存在）
├── san              ← San 源码仓库（已存在，被管理的目标项目）
└── san-team         ← 新建的仓库（纯内容，无 Go 代码）
    ├── leader/
    │   ├── system/
    │   │   ├── identity.md
    │   │   ├── behavior.md
    │   │   └── rules.md
    │   ├── skills/
    │   └── settings.json
    ├── dev/
    │   ├── system/
    │   │   ├── identity.md
    │   │   ├── behavior.md
    │   │   └── rules.md
    │   ├── skills/
    │   └── settings.json
    ├── qe/
    │   ├── system/
    │   │   ├── identity.md
    │   │   ├── behavior.md
    │   │   └── rules.md
    │   ├── skills/
    │   └── settings.json
    ├── release/
    │   ├── system/
    │   │   ├── identity.md
    │   │   ├── behavior.md
    │   │   └── rules.md
    │   ├── skills/
    │   └── settings.json
    ├── state/             ← queue.jsonl 存放位置
    └── run.sh             ← 轮询循环（12 行 shell 脚本）
```

- **`san-team`**：纯内容仓库 —— 无 Go 代码、无二进制。只包含
  persona markdown 文件、共享状态目录和轮询 shell 脚本。
  所有 Go 逻辑都在 San 中，作为可复用的独立功能。
- **团队（Team）**：`san-team` 仓库中的一组 Persona，
  为特定目标协同工作 —— 管理 San 项目的 issue、feature、bug 和发布。
- **Persona 目录**：每个 Persona 遵循 persona 规范的三层结构：
  1. `system/` — 系统提示词，拆为 `identity`（是谁）、`behavior`（怎么做事）、`rules`（遵守什么规则）三个可替换的 md 文件，第四个 `environment` 由运行时计算
  2. `skills/` — 该 Persona 专属的技能，启动时自动激活
  3. `settings.json` — 配置覆盖：工具、权限、模型、max_steps 等

## 运行模型

每个 Persona 以**独立的 San 实例**运行。根据角色不同，启动方式有区别：

### Leader —— 交互式 TUI

Leader 是管理员的对话入口，以标准交互式 San 会话运行：

```bash
san --persona leader
```

使用 San 现有的 `--persona` 参数，从 `~/.san/personas/leader/`
（指向 `san-team/leader/` 的软链接）加载 persona 配置。

### Dev, QE, Release —— 通过 run.sh 无头运行

这些 persona 通过 `run.sh` 启动，以 `san --persona <name> -p` 方式无头运行。
不需要交互式终端 —— 它们轮询队列、领取任务、执行任务、更新队列：

```bash
# 终端 2：启动 Dev 轮询循环
./run.sh dev /path/to/san

# 终端 3：启动 QE 轮询循环
./run.sh qe /path/to/san

# 终端 4：启动 Release 轮询循环
./run.sh release /path/to/san
```

`run.sh` 是一个简单的 shell 脚本（见下文）。每个 persona 的系统提示词
（identity + behavior + rules）由 `san --persona <name> -p` 从
`~/.san/personas/<name>/` 加载。

### 安装：链接 Persona

启动前，将 san-team 中的 persona 链接到 San 的 persona 目录：

```bash
ln -s /path/to/san-team/leader  ~/.san/personas/leader
ln -s /path/to/san-team/dev     ~/.san/personas/dev
ln -s /path/to/san-team/qe      ~/.san/personas/qe
ln -s /path/to/san-team/release ~/.san/personas/release
```

所有 persona —— Leader、Dev、QE、Release —— 统一使用 `~/.san/personas/` 机制。
不需要额外的 persona 加载代码。

## run.sh —— 粘合层

`run.sh` 是 san-team 中唯一的"代码"。它是 `san queue` 和 `san -p` 之间的桥梁：

```bash
#!/bin/bash
set -euo pipefail
TEAM_DIR="$(cd "$(dirname "$0")" && pwd)"
PERSONA="${1:?usage: $0 <leader|dev|qe|release>}"
CWD="${2:-$(pwd)}"
INTERVAL="${3:-30}"

echo "[san-team:$PERSONA] 每 ${INTERVAL}s 轮询一次, cwd=$CWD"

while true; do
  # 1. 原子认领下一个 pending 任务
  TASK=$(san queue claim --dir "$TEAM_DIR/state" --role "$PERSONA" --persona "$PERSONA" 2>/dev/null || true)
  if [ -n "$TASK" ]; then
    ID=$(echo "$TASK" | jq -r '.id')
    TITLE=$(echo "$TASK" | jq -r '.title')
    DESC=$(echo "$TASK" | jq -r '.description')
    echo "[san-team:$PERSONA] 认领 $ID: $TITLE"

    # 2. 构建任务提示词，无头运行 Agent
    PROMPT="Task: $TITLE

    $DESC

    完成后，总结你的工作内容和 PR 链接。"

    if san --persona "$PERSONA" -p "$PROMPT"; then
      san queue complete --dir "$TEAM_DIR/state" --id "$ID" --persona "$PERSONA"
      echo "[san-team:$PERSONA] 完成 $ID"
    else
      san queue release --dir "$TEAM_DIR/state" --id "$ID" --persona "$PERSONA"
      echo "[san-team:$PERSONA] 释放 $ID（将重试）"
    fi
  fi
  sleep "$INTERVAL"
done
```

关键设计要点：
- **队列操作是 shell 的职责**：`run.sh` 调用 `san queue claim`、
  `san queue complete`、`san queue release`。LLM Agent 完全不知道队列的存在。
- **Persona 加载是 San 的职责**：`san --persona <name> -p` 从
  `~/.san/personas/` 加载 persona 系统提示词。Shell 不需要拼装 persona 文件。
- **LLM 推理是 San 的职责**：`san -p` 用任务描述作为用户消息来执行 Agent。
- **Shell 只是胶水**：认领 → 运行 → 完成/释放。12 行代码。

## 工作流程

管理员只跟 Leader Persona 对话。Leader 把需求拆成 Task，
通过 `san queue add` 写入共享队列。其他 Persona 通过
`run.sh` 轮询队列，认领匹配的任务，完成后更新队列状态。

```
管理员（人）
    │
    │  "实现用户登录功能"
    ▼
┌────────────────────────────────────────────┐
│ Leader（san --persona leader）             │
│                                            │
│ 1. 理解需求                                │
│ 2. 画架构图、状态图（向管理员确认）        │
│ 3. 拆解为 Task                             │
│ 4. 写入队列：san queue add ...             │
│ 5. 监控：san queue list ...                │
└────────────────┬───────────────────────────┘
                 │  共享工作队列 (state/queue.jsonl)
        ┌────────┼────────┐
        ▼        ▼        ▼
  ┌──────────┐ ┌──────┐ ┌─────────┐
  │ run.sh   │ │run.sh│ │ run.sh  │
  │   dev    │ │  qe  │ │ release │
  │          │ │      │ │         │
  │ claim→   │ │claim→│ │claim→   │
  │ san -p   │ │agent │ │ san -p  │
  │ --persona│ │run   │ │--persona│
  │   dev    │ │--qe  │ │ release │
  │ →complete│ │→verify│ │→complete│
  └──────────┘ └──────┘ └─────────┘
```

### Leader Persona —— 唯一入口

Leader 通过 `san --persona leader` 启动，
是管理员唯一的交互界面。管理员告诉 Leader 要做什么，
Leader 负责：

1. **理解需求**：新功能？Bug 修复？重构？
2. **分析 San 项目**：读取 San 源码仓库中的设计文档和现有代码
3. **可视化**：画 mermaid 架构图、状态图，向管理员确认理解
4. **拆解**：把功能拆成多个 Task，通过 `san queue add --dir state/ --role dev --title "..." --description "..."` 写入队列
5. **监控**：通过 `san queue list --dir state/` 跟踪队列中的 Task 状态
6. **汇报**：收集完成结果，向管理员汇总

Leader 不自己写代码。Leader 把 Task 写入队列后，
由对应 Persona 的 `run.sh` 循环自动领取执行。

```
Leader 派发编码任务示例：

Leader:
  1. 分析需求后，确定 Task-3 是编码任务
  2. 运行：san queue add --dir state/ --role dev --title "实现 JWT..."
  3. Dev 的 run.sh 轮询队列，发现 Task-3 匹配自己的角色
  4. Dev 通过 san queue claim 认领，运行 san --persona dev -p ".."
  5. Agent 完成，run.sh 调用 san queue complete 附上 PR 链接
  6. Leader 通过 san queue list 看到 Task-3 已完成
```

### Dev Persona —— 编码实现

由 `run.sh dev` 启动，持续轮询队列中的编码任务：

1. `run.sh` 通过 `san queue claim` 认领 role 为 dev 的 Task
2. 从任务描述构建提示词
3. 运行 `san --persona dev -p "..."
4. Agent 读取 San 项目的设计文档和现有代码
5. Agent 遵循分层架构规范写代码
6. Agent 写单元测试（同包内）
7. Agent 跑 `make test` + `make lint` 通过
8. Agent 提交代码、创建 PR
9. 成功：`run.sh` 调用 `san queue complete --id $ID --pr <url>`
10. 失败：`run.sh` 调用 `san queue release --id $ID`

Dev Agent 完全不接触队列 —— `run.sh` 根据 Agent 的退出码处理所有队列操作。

### QE Persona —— 验证测试

由 `run.sh qe` 启动，持续轮询队列中的验证任务：

1. `run.sh` 认领 role 为 qe 的 Task
2. 运行 `san --persona qe -p "..."
3. Agent 检出 PR 分支
4. Agent 跑全量测试 + lint + layer check
5. Agent 在 `test-integration/` 中添加集成测试，提交到同一 PR
6. Agent 发 PR Review
7. 通过：`run.sh` 调用 `san queue verify --id $ID --result "passed"`
8. 失败：`run.sh` 调用 `san queue release --id $ID --reason "..."`

QE 也可以在开发前介入：Leader 写好架构图后，先创建验证设计的 Task 给 QE。

### Release Persona —— 发布上线

由 `run.sh release` 启动，持续轮询队列中的发布任务：

1. `run.sh` 认领 role 为 release 的 Task
2. 运行 `san --persona release -p "..."
3. Agent 生成 CHANGELOG
4. Agent 更新版本号
5. Agent 打 Git Tag
6. Agent 生成 Release Notes
7. 成功：`run.sh` 调用 `san queue complete --id $ID`

### 共享工作队列

队列是 Persona 之间的唯一通信机制，文件位于
`san-team/state/queue.jsonl`（JSONL 追加日志）。
所有操作通过 `san queue` 子命令完成（详见 [ADR-0003](0003-shared-work-queue.md)）。

```
type WorkItem struct {
    ID          string       // 唯一标识
    Role        string       // dev / qe / release
    Title       string       // 任务标题
    Description string       // 任务描述（Leader 填充）
    Status      ItemStatus   // pending → claimed → done → verified
    AssignedTo  string       // 认领的 Persona 名称
    PR          string       // PR 链接（Dev 填充）
    Result      string       // 结果说明（QE/Release 填充）
    CreatedAt   time.Time
    UpdatedAt   time.Time
    ClaimedAt   time.Time
    RetryCount  int
    MaxRetries  int
}
```

状态流转：

```
pending ──claim()──→ claimed ──complete()──→ done ──verify()──→ verified
   ↑                     │                       │
   └──release()─── ← (超时)              ← reject() ──┘
                         │
                         └──fail()──→ failed
```

## 完整安装运行示例

从零到团队运行的完整步骤。

### 前提条件

```bash
# San CLI 已编译且在 PATH 中
cd /path/to/san
make build
export PATH="$PATH:$PWD/bin"

# san-team 仓库克隆到 san 同级目录
cd /path/to
git clone git@github.com:genai-io/san-team.git
```

### 第一步：链接 Persona

```bash
ln -s /path/to/san-team/leader  ~/.san/personas/leader
ln -s /path/to/san-team/dev     ~/.san/personas/dev
ln -s /path/to/san-team/qe      ~/.san/personas/qe
ln -s /path/to/san-team/release ~/.san/personas/release
```

### 第二步：启动 Leader（交互式）

```bash
# 终端 1：Leader 会话
cd /path/to/san
san --persona leader
```

管理员在这里与 Leader 交互。例如：

```
管理员：实现用户认证功能，支持 JWT 登录

Leader：
  读取 docs/design/ → 分析代码库 → 画时序图
  与管理员确认 → 拆解为 Task：

  $ san queue add --dir /path/to/san-team/state --role dev \
      --title "定义 User 模型和 UserStore 接口" \
      --description "在 internal/core/user.go 中创建 User struct..."

  $ san queue add --dir /path/to/san-team/state --role dev \
      --title "基于 SQLite 实现 UserStore" \
      --description "在 internal/feature/userstore/ 中实现 UserStore 接口..."

  $ san queue add --dir /path/to/san-team/state --role dev \
      --title "实现 JWT token 生成与验证" \
      --description "创建 internal/core/jwt/ 包..."

  $ san queue add --dir /path/to/san-team/state --role dev \
      --title "实现 POST /auth/login handler" \
      --description "创建登录 API handler..."

  $ san queue add --dir /path/to/san-team/state --role qe \
      --title "验证认证模块（Task 1-4）" \
      --description "检出各 PR，运行测试，添加集成测试..."

  $ san queue add --dir /path/to/san-team/state --role release \
      --title "发布 v1.3.0" \
      --description "生成 CHANGELOG，更新版本号，打 tag v1.3.0..."
```

### 第三步：启动工作 Persona（无头模式）

```bash
# 终端 2：Dev 轮询循环
cd /path/to/san-team
./run.sh dev /path/to/san 30

# 终端 3：QE 轮询循环
cd /path/to/san-team
./run.sh qe /path/to/san 30

# 终端 4：Release 轮询循环
cd /path/to/san-team
./run.sh release /path/to/san 60
```

### 第四步：查看进度

```bash
# 任意终端：查看队列状态
san queue list --dir /path/to/san-team/state
```

输出示例：

```
ID        Role     标题                              状态      负责人    PR
a1b2c3d4  dev      定义 User 模型和 UserStore 接口    done      dev       #123
b2c3d4e5  dev      实现 UserStore（SQLite）           done      dev       #124
c3d4e5f6  dev      实现 JWT token 生成                claimed   dev       -
d4e5f6a7  dev      实现 POST /auth/login              pending   -         -
a7b8c9d0  qe       验证认证模块（Task 1-4）           pending   -         -
b8c9d0e1  release  发布 v1.3.0                        pending   -         -

Total: 6 | pending: 3 | claimed: 1 | done: 2 | verified: 0 | failed: 0
```

### run.sh 内部运行过程

当 Dev 的 `run.sh` 轮询发现一个 pending 任务时：

```
1. san queue claim --dir state/ --role dev --persona dev
   → 返回 JSON: {"id":"d4e5f6a7...", "title":"实现 POST /auth/login", ...}
   → 任务状态变为 "claimed"

2. san --persona dev -p ".." --prompt "Task: 实现 POST /auth/login
   创建登录 API handler..." --cwd /path/to/san
   → Agent 从 ~/.san/personas/dev/ 加载 dev persona
   → Agent 实现 handler + 单元测试
   → Agent 创建 PR #126
   → Agent 退出码 0（成功）

3. san queue complete --dir state/ --id d4e5f6a7 --persona dev \
     --pr "https://github.com/genai-io/san/pull/126"
   → 任务状态变为 "done"

4. sleep 30，重复
```

如果 Agent 执行失败：

```
1. san queue claim ...  （同上）
2. san --persona dev -p "..."
   → Agent 遇到错误，退出码 1（失败）
3. san queue release --dir state/ --id d4e5f6a7 --persona dev
   → 任务状态回退 "pending"，retryCount++
   → 下次轮询会再次认领（最多重试 maxRetries 次）
```

## 完整工作流示例

管理员输入：**"实现用户认证功能，支持 JWT 登录"**

### 1. Leader 理解需求 + 画架构图

Leader 读取 San 项目的 `docs/design/`，分析已有代码，
生成 mermaid 时序图：

```mermaid
sequenceDiagram
    participant Client
    participant AuthHandler
    participant AuthService
    participant UserStore
    participant JWT

    Client->>AuthHandler: POST /auth/login
    AuthHandler->>AuthService: Login(username, password)
    AuthService->>UserStore: FindByUsername(username)
    UserStore-->>AuthService: User
    AuthService->>AuthService: Verify password
    AuthService->>JWT: GenerateToken(user)
    JWT-->>AuthService: token
    AuthService-->>AuthHandler: token
    AuthHandler-->>Client: 200 { token }
```

Leader 展示图给管理员："这是我理解的功能流程，对吗？"

### 2. 拆解 Task 并写入队列

管理员确认后，Leader 将 Task 写入共享队列：

```
$ san queue add --dir state/ --role dev --title "定义 User 模型和 UserStore 接口" \
    --description "在 internal/core/user.go 中创建 User struct（ID, Username, PasswordHash, CreatedAt）..."

$ san queue add --dir state/ --role dev --title "基于 SQLite 实现 UserStore" \
    --description "在 internal/feature/userstore/ 中实现 UserStore 接口..."

$ san queue add --dir state/ --role dev --title "实现 JWT token 生成与验证" \
    --description "创建 internal/core/jwt/ 包..."

$ san queue add --dir state/ --role dev --title "实现登录 API Handler" \
    --description "在 internal/app/authhandler/ 中创建登录 API handler..."

$ san queue add --dir state/ --role qe --title "验证全部认证功能" \
    --description "检出 PR，运行测试，添加集成测试..."

$ san queue add --dir state/ --role release --title "发布 v1.3.0" \
    --description "生成 CHANGELOG，更新版本号，打 tag v1.3.0..."
```

### 3. 各 Persona 自动领取执行

```
Dev 的 run.sh 轮询队列：
  认领 Task 1 → san --persona dev -p ".." → 实现 → PR #123 → complete
  认领 Task 2 → san --persona dev -p ".." → 实现 → PR #124 → complete
  认领 Task 3 → san --persona dev -p ".." → 实现 → PR #125 → complete
  认领 Task 4 → san --persona dev -p ".." → 实现 → PR #126 → complete

QE 的 run.sh 轮询队列：
  发现 Task 1-4 均为 done → 认领 Task 5
  san --persona qe -p ".." → 检出 PR → 运行测试 → 添加集成测试
  → 通过 → san queue verify

Leader 监控到全部 verified → 通知管理员 approve PR
管理员审批通过 → Leader 运行：
  $ san queue add --dir state/ --role release --title "发布 v1.3.0" ...

Release 的 run.sh 轮询队列：
  认领 Task 6 → san --persona release -p ".." → changelog → tag → complete

Leader → 管理员: "认证功能已全部完成并发布，PR: #123-#126"
```

## Bug 修复流程

管理员对 Leader 说：**"扫描并修复所有 P0 Bug"**

```
Leader:
  1. 通过 gh 拉取 San 项目所有 P0 Bug issues
  2. 逐个分析，写入队列：
     $ san queue add --dir state/ --role dev --title "修复 #100 nil pointer in auth.go" ...
     $ san queue add --dir state/ --role dev --title "修复 #102 timeout in db query" ...

Dev 的 run.sh：
  认领 "#100" → san --persona dev -p ".." → 修复 → PR → complete
  认领 "#102" → san --persona dev -p ".." → 修复 → PR → complete

QE 的 run.sh：
  认领 "#100 验证" → san --persona qe -p ".." → 验证 → 通过
  认领 "#102 验证" → san --persona qe -p ".." → 验证 → 通过

Leader 通知管理员 approve → 管理员审批通过

Release 的 run.sh：
  认领 "hotfix 发布" → changelog → tag → complete

Leader → 管理员: "2 个 P0 Bug 已全部修复并发布"
```

## Persona 配置示例

每个 Persona 是一个遵循 persona 规范的目录。以 Dev 为例：

### system/identity.md（Who am I?）

```markdown
你是 San 项目的开发 Agent。
你的职责是实现分配给你的编码任务。
你擅长 Go 开发，熟悉 San 的五层包架构。
```

### system/behavior.md（How do I act?）

```markdown
## 工作方式

1. 先读取相关的设计文档和现有代码，理解上下文后再动手
2. 遵循 internal/ 五层架构的依赖方向：cmd → app → feature → core → infrastructure
3. 每个变更必须在同包内包含单元测试
4. 完成后运行 make test 和 make lint，确保通过后再提交
5. 用清晰的信息提交代码、创建 PR，报告 PR 链接

## 沟通风格

- 遇到不确定的设计决策，在结果中记录问题供 Leader 参考
- 完成后在结果中附上简洁明确的说明：做了什么、改了哪些文件、PR 链接
- 不要做超出 Task 范围的事
```

### system/rules.md（What rules do I follow?）

```markdown
## 安全约束

- 不得修改 .env、credentials、密钥文件
- 不得执行 rm -rf、force push 等破坏性操作
- 不得跳过 git hooks（--no-verify）

## Git 规范

- 分支命名：feat/<issue>-<slug> 或 fix/<issue>-<slug>
- Commit message 遵循项目规范
- PR 描述中引用对应的 issue

## 代码规范

- 新增包必须有对应的 docs/packages/<pkg>.md
- 接口变更必须更新 Contract 章节
- 不得引入循环依赖
```

### settings.json

```json
{
  "description": "San 项目开发 Persona，负责编码和 PR 提交",
  "model": "claude-sonnet-4-6",
  "maxSteps": 80,
  "skills": {
    "code-review": "active",
    "simplify": "active"
  },
  "pollInterval": "30s",
  "disabledTools": {},
  "permissions": {
    "defaultMode": "acceptEdits",
    "allow": [
      "Bash(make:*)",
      "Bash(go:*)",
      "Bash(git:*)",
      "Bash(gh:*)"
    ],
    "deny": [
      "Bash(rm -rf:*)",
      "Bash(git push --force:*)",
      "Bash(git reset --hard:*)"
    ]
  }
}
```

### Leader 的 system/identity.md

```markdown
你是 San 项目的 Leader Agent，是管理员唯一的交互入口。
你负责理解需求、分析项目、画架构图、拆解任务并写入共享队列。
你自己不写业务代码，你的职责是规划、编排和决策。
```

### Leader 的 settings.json

```json
{
  "description": "San 项目 Leader Persona，管理员的唯一入口",
  "model": "claude-opus-4-7",
  "maxSteps": 200,
  "skills": {},
  "pollInterval": "10s",
  "permissions": {
    "defaultMode": "acceptEdits",
    "allow": [
      "Bash(make:*)",
      "Bash(go:*)",
      "Bash(git:*)",
      "Bash(gh:*)"
    ],
    "deny": [
      "Bash(rm -rf:*)",
      "Bash(git push --force:*)"
    ]
  }
}
```

## 关键设计决策

### 1. Persona 目录

Persona 遵循 [`persona-system.md`](../../notes/active/persona-system.md) 中定义的 persona 规范。
每个 Persona 是一个文件夹，包含 `system/`（拆为 identity/behavior/rules/environment 四部分）、
`skills/`、`settings.json`。缺失部分自动回退到 San 内置默认值。

### 2. Persona 存于独立的 `san-team` 仓库

Persona 定义放在独立仓库 `san-team`，与 San 源码仓库分离：
- Persona 配置独立版本管理
- 可以对 san-team 仓库设置不同的访问权限
- 团队 Persona 可以管理多个目标仓库（未来扩展）

### 3. san-team 是纯内容仓库 —— 无 Go 代码

san-team 只包含 markdown 文件、`state/` 和 shell 脚本。所有 Go 逻辑都在 San 中，
作为可复用的独立功能：
- `san queue` —— 原子工作队列操作（详见 [ADR-0003](0003-shared-work-queue.md)）
- `san --persona <name> -p` —— 带 persona 系统提示词的无头 Agent

这种分离意味着 san-team 没有构建步骤、没有依赖，任何人都可以修改 markdown 和 shell 脚本。

### 4. Leader 是唯一入口

管理员不直接跟 Dev/QE/Release 交互，所有指令给 Leader：
- 管理员心智模型简单：只有一个对话对象
- Leader 有全局视角，决定优先级和冲突处理
- 其他 Persona 只关注队列中的任务，无需理解全局

### 5. 每个 Persona 是独立的 San 实例

每个 Persona 启动一个独立的 San 进程：
- **Leader**：`san --persona leader` — 交互式 TUI，与管理员对话
- **Dev/QE/Release**：`san --persona <name> -p` — 无头模式，由 `run.sh` 启动

优点：
- 进程级隔离：每个 Persona 有独立的上下文、工具集、权限
- 可以部署在不同机器/容器上，独立扩缩
- 通过共享工作队列（文件）协调，不需要进程间 RPC
- 所有 persona 加载统一在 `~/.san/personas/` 下

### 6. 架构图是沟通语言

Leader 在动手之前先画 mermaid 图：
- 管理员确认理解（避免做错方向）
- Dev Persona 有清晰的实现参考（图随 Task 描述一起写入队列）
- QE Persona 有验证依据（按图检查完整性）

### 7. Persona 自我进化

每个 Persona 在项目中持续学习和自我改进。进化过程通过更新
`san-team` 仓库中的 Persona 配置来固化。

**学习来源：**
- 每次 Task 完成后，Persona 记录复盘：哪些做得好、哪些可改进
- QE 发现的失败模式自动反馈给 Dev，更新其 `behavior.md` 或 `rules.md`
- Leader 观察各 Persona 的表现，定期优化配置

**进化内容：**

| 进化项 | 方式 | 示例 |
|---|---|---|
| Skills | 发现好用的技能 → 写入 `settings.json` 的 `skills` 字段 | QE 发现 `bug-hunt` 效果很好 → 设为 `active` |
| 权限 | 发现权限不足 → Leader 评估后更新 `permissions.allow` | Dev 需要新工具 → Leader 审批后追加 |
| 行为规范 | 从失败中总结规则 → 更新 `system/rules.md` | 多次因为缺少测试被 QE 驳回 → 强化测试规则 |
| 工作流程 | 发现效率瓶颈 → 更新 `system/behavior.md` | 发现先读设计文档再写代码效率更高 → 固化为行为规范 |

**进化流程：**
```
Task 完成 → Persona 写复盘记录 → 发现可改进项
  → Persona 向 Leader 提改进建议
  → Leader 审批
  → 更新 san-team 仓库中的 Persona 配置（通过 PR）
  → 下次启动时自动加载新配置
```

**安全约束：**
- 权限变更必须由 Leader 审批，Persona 不能自行提权
- 所有配置变更走 Git PR，有历史记录可追溯
- 管理员可以随时回滚到之前的配置版本

## 与现有架构的关系

| 新概念 | 对应现有 / 规划中的机制 |
|---|---|
| Persona 目录 | `~/.san/personas/`（现有 persona 系统） |
| `san --persona leader` | 现有 `--persona` 参数（无变化） |
| `san --persona <name> -p` | 现有：`-p` 打印模式配合 `--persona`（PR #231） |
| 共享工作队列 | 新增：`san queue` 子命令（详见 ADR-0003） |
| run.sh 轮询循环 | san-team 中 12 行 shell 脚本 |
| Persona 通信 | 通过 `san queue` 队列轮询，无需进程间 RPC |
| Persona 权限 | settings.json 中 permissions（现有机制） |

## 实现计划

### 阶段一：创建 `san-team` 仓库

- 在 `genai-io` Org 下创建 `san-team` 仓库
- 按 persona 规范编写四套 Persona（leader/dev/qe/release）
- 每套包含 `system/{identity,behavior,rules}.md` + `skills/` + `settings.json`
- 创建 `run.sh` 轮询脚本
- 创建 `state/` 目录（queue.jsonl 在运行时创建）

### 阶段二：添加 `san queue` 子命令

- 实现 `internal/queue/` 包（item.go, queue.go, claim.go）— ~200 行
- 接入 `cmd/san/queue.go` cobra 子命令
- 命令：list, claim, add, complete, verify, release, fail
- 详见 [ADR-0003](0003-shared-work-queue.md)

### 阶段三：`--persona` + `-p` 集成（已完成，PR #231）

- 修复 `runPrint`，使 `--persona` 和 `-p` 同时提供时加载 persona 系统提示词
- 从 `~/.san/personas/<name>/` 加载 persona，通过 `system.Build()` 构建包含了
  persona identity/behavior/rules 的系统提示词
- 应用 persona 的 settings.json 中的 model 和 disabledTools
- 这替代了 `run.sh` 需要拼装 persona 文件到 prompt 的需求

### 阶段四：端到端工作流串联

- Leader: `san --persona leader` → 理解需求 → 拆解 → `san queue add`
- Dev: `run.sh dev` → `san queue claim` → `san --persona dev -p ".."` → `san queue complete`
- QE: `run.sh qe` → `san queue claim` → `san --persona qe -p ".."` → `san queue verify`
- Release: `run.sh release` → `san queue claim` → `san --persona release -p ".."` → `san queue complete`
- Leader: `san queue list` → 监控 → 向管理员汇报

### 阶段五：自动触发与运维

- Cron 定时 Bug 扫描 → 自动写入队列
- 设计文档合并后自动触发 Leader 拆解
- 进度可视化（`san queue list`）
- `san queue compact` 用于 JSONL 压缩

## 参考技能

以下是从 GitHub 社区收集的现有技能，各 Persona 可以直接复用或参考其设计。

### Leader Persona 相关

| 技能 | 来源 | Stars | 用途 |
|---|---|---|---|
| `/plan` + `/work` | [jdelfino/agent-workflow](https://github.com/jdelfino/agent-workflow) | 9 | 将需求拆解为带依赖的子任务，创建 Epic + Subtask，通过 beads 追踪依赖关系 |
| `/todo-task-planning` + `/todo-task-run` | [gendosu/agent-skills](https://github.com/gendosu/agent-skills) | 0 | 两阶段工作流：分析需求 → 生成结构化 TODO.md → 逐步执行并提交 PR |
| `/backlog:plan` + `/backlog:standup` | [backloghq/backlog](https://github.com/backloghq/backlog) | 3 | 跨会话持久化任务管理，支持依赖、优先级、due date，agent 之间可交接 |
| `/dev` (6-phase SOP) | [hnaymyh123-henry/claude-dev-skill](https://github.com/hnaymyh123-henry/claude-dev-skill) | 112 | Tech Lead 模式：需求分析 → PRD 对齐 → 架构决策 → 并行开发 → QA → CR + 合并 |
| `project-manager` | [gendosu/agent-skills](https://github.com/gendosu/agent-skills) | 0 | 项目管理和任务组织 |

### Dev Persona 相关

| 技能 | 来源 | Stars | 用途 |
|---|---|---|---|
| `implementer` | [jdelfino/agent-workflow](https://github.com/jdelfino/agent-workflow) | 9 | test-first 开发，在独立 worktree 中实现，完成后触发并行 review |
| `git-operations-specialist` | [gendosu/agent-skills](https://github.com/gendosu/agent-skills) | 0 | Git 历史分析、冲突解决、分支策略、GitHub CLI 操作 |
| `micro-commit` | [gendosu/agent-skills](https://github.com/gendosu/agent-skills) | 0 | 遵循 micro-commit 方法论做细粒度提交 |
| 并行 Worker Agent | [hnaymyh123-henry/claude-dev-skill](https://github.com/hnaymyh123-henry/claude-dev-skill) | 112 | 多个 Worker Agent 在隔离 worktree 中并行开发，含 6 类边界条件自检 |

### QE Persona 相关

| 技能 | 来源 | Stars | 用途 |
|---|---|---|---|
| `bug-hunt` (Hunter + Skeptic + Referee) | [danpeg/bug-hunt](https://github.com/danpeg/bug-hunt) | 142 | 对抗式 Bug 发现：Hunter 过度报告 → Skeptic 尝试驳回 → Referee 独立裁决 |
| Jenny (实现验证) | [darcyegb/ClaudeCodeAgents](https://github.com/darcyegb/ClaudeCodeAgents) | 716 | 对照项目 Spec 检查实现完整性 |
| Karen (Reality Check) | [darcyegb/ClaudeCodeAgents](https://github.com/darcyegb/ClaudeCodeAgents) | 716 | 诚实评估项目完成状态，识别半成品 |
| Code Quality Pragmatist | [darcyegb/ClaudeCodeAgents](https://github.com/darcyegb/ClaudeCodeAgents) | 716 | 检测过度工程、不必要的抽象、过早优化 |
| Task Completion Validator | [darcyegb/ClaudeCodeAgents](https://github.com/darcyegb/ClaudeCodeAgents) | 716 | 验证标记为 "done" 的功能是否端到端可用 |
| UI Comprehensive Tester | [darcyegb/ClaudeCodeAgents](https://github.com/darcyegb/ClaudeCodeAgents) | 716 | 跨平台 UI 测试（Puppeteer/Playwright） |
| `correctless` (32 skills) | [joshft/correctless](https://github.com/joshft/correctless) | - | Spec 驱动的 TDD，Agent 分离原则：Spec Agent → Test Agent → Implementation Agent → QA Agent → Verification Agent |
| `skill-test` pipeline | [easyfan/skill-test](https://github.com/easyfan/skill-test) | - | 测试流水线协调器：静态 review → 行为 eval → 部署验证 |
| 4 类 Reviewer (correctness/tests/architecture/plan) | [jdelfino/agent-workflow](https://github.com/jdelfino/agent-workflow) | 9 | 并行 Code Review，分别检查正确性、测试覆盖、架构一致性、计划符合度 |
| QA Agent (Phase 3.5) | [hnaymyh123-henry/claude-dev-skill](https://github.com/hnaymyh123-henry/claude-dev-skill) | 112 | 6 类边界条件自检（Null/Empty/Boundary/External failure/Concurrency/Malicious input） |

### 如何集成

1. **直接复用**：将对应仓库的 `.claude/skills/` 目录拷贝到 Persona 的 `skills/` 目录下
2. **参考设计**：提取其 Agent 分离、对抗式验证、多阶段审查等模式，重写为适合 San 项目的版本
3. **优先级**：社区技能（marketplace scope）优先级低于项目自定义技能，需要 Leader 审批后启用

## 已决策补充

1. **多 Dev 并行**：每个 Dev San 实例使用独立的 git worktree
   工作，各自提交 PR。如果多个 PR 修改同一文件，由 GitHub 的 merge conflict
   机制处理。Leader 发现冲突时重新分配修复任务。
2. **Leader 合并权限**：所有代码必须由管理员手动 approve 才能合并，不允许自动 merge。
   QE 通过后 Leader 通知管理员来审批，管理员点一下 approve 就行。
3. **失败重试策略**：由 Leader 决定每个 Task 的最大重试次数（默认 3 次）。
   Dev 重试耗尽仍未通过 QE 时，`run.sh` 调用 `san queue fail`，
   将 Task 标记为 failed。Leader 检测到后通知管理员介入。
4. **崩溃恢复**：所有异常情况（Persona 实例崩溃、Leader 崩溃、队列文件损坏等）
   都必须妥善处理，并向管理员汇报当前状态。包括：
   - Persona 崩溃：claimed 状态 Task 超时自动回退 pending，Leader 检测到后通知管理员
   - Leader 崩溃：管理员重新启动 Leader 后，Leader 回放队列恢复上下文
   - 队列文件损坏：从 Git 历史恢复最近一次正常快照
   - `run.sh` 崩溃：直接重启脚本即可，从上次中断处继续（已认领的 Task 超时后自动回退）


## 参考资料

- [`persona-system.md`](../../notes/active/persona-system.md) — Persona 目录遵循的 persona 设计规范
- [ADR-0003](0003-shared-work-queue.md) — 共享工作队列设计（`san queue`）
- [`san-team/DESIGN.md`](../../../san-team/DESIGN.md) — prompt-first san-team 设计
- [`san-team/state/EXAMPLE.md`](../../../san-team/state/EXAMPLE.md) — 队列示例
- [`core.Agent`](../../packages/core.md) — 底层 Agent 原语
- [`packages/subagent.md`](../../packages/subagent.md) — 子 Agent 机制
- [`packages/skill.md`](../../packages/skill.md) — 技能加载
- [`concepts/permission-model.md`](../../concepts/permission-model.md) — 权限模型
- [`ADR-0001`](0001-layered-package-architecture.md) — 分层包架构
