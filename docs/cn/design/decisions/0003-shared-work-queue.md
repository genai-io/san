# ADR-0003：多角色协调共享工作队列

## 状态

提议 — 2026-06-16。

## 背景

ADR-0002 定义了一个自主开发管理模型，多个角色（Leader、Dev、QE、Release）以独立的
San 实例运行，通过共享工作队列进行协调。

队列需要满足以下条件：
- 允许多个独立进程安全地领取和更新任务，无需中心服务器或数据库
- 人类可读、可 Git 版本控制，以支持崩溃恢复
- 支持任务生命周期状态机：pending → claimed → done → verified
- 可与 shell 脚本（`run.sh` 轮询循环）配合使用，无需在 team 仓库中放置 Go 代码

## 决策

在 San CLI 中新增 `san queue` 子命令，实现**基于文件的 JSONL 工作队列**。

### 与 Persona 系统的关系

`san queue` 和 `san --persona <name> -p` 是独立、可组合的功能：

- **`san queue`** 管理共享工作队列：add、claim、complete、verify、release、fail、
  list。它完全不感知 persona 或 LLM Agent。
- **`san --persona <name> -p`** 从 `~/.san/personas/` 加载 persona，
  注入其系统提示词（identity + behavior + rules），无头运行 Agent。
  它完全不知道队列的存在。

san-team 中的 `run.sh` 脚本将两者组合在一起：

```
run.sh → san queue claim → san --persona  -p ".."→ san queue complete/release
```

这种分离意味着 `san queue` 可以独立于 san-team 使用 —— 任何多 Agent 编排场景、
CI 流水线或任务调度器都可以复用。

### 存储格式

```
<team-dir>/state/queue.jsonl
```

每行是一个紧凑的 JSON 对象。状态变更追加新行（相同 ID，更新字段）。读取时按 ID
去重，保留最后一行。这样队列可以轻松从 Git 历史中恢复（`git checkout --
state/queue.jsonl`）。

### 工作项结构

```json
{
  "id": "a1b2c3d4e5f6a7b8",
  "role": "dev",
  "title": "实现 JWT 令牌生成",
  "description": "创建 internal/core/jwt/ 包...",
  "status": "claimed",
  "assignedTo": "dev",
  "pr": "",
  "result": "",
  "createdAt": "2026-06-16T10:00:00Z",
  "updatedAt": "2026-06-16T10:30:00Z",
  "claimedAt": "2026-06-16T10:30:00Z",
  "retryCount": 0,
  "maxRetries": 3
}
```

### 状态机

```
pending ──claim()──→ claimed ──complete()──→ done ──verify()──→ verified
   ↑                     │                       │
   └──release()─── ← (超时)              ← reject() ──┘
                         │
                         └──fail()──→ failed
```

- **claim**：原子"读→找 pending→追加 claimed"操作。通过对 `<dir>/queue.lock` 加
  `flock` 防止两个进程领取同一任务。
- **release**：将 claimed 退回 pending，重试计数 +1。
- **fail**：当 retryCount >= maxRetries 时，标记为永久失败。
- **超时释放**：claimed 超过 10 分钟的任务自动退回 pending。

### CLI 接口

```
san queue list     --dir <state-dir> [--role <r>] [--status <s>]
san queue claim    --dir <state-dir> --role <r> --persona <n>
san queue add      --dir <state-dir> --role <r> --title "..." --description "..."
san queue complete --dir <state-dir> --id <id> --persona <n> [--pr <url>] [--result "..."]
san queue verify   --dir <state-dir> --id <id> --persona <n> [--result "..."]
san queue release  --dir <state-dir> --id <id> --persona <n> [--reason "..."]
san queue fail     --dir <state-dir> --id <id> --persona <n> [--reason "..."]
```

注意：没有 `san queue prompt` 命令。提示词构建由 `run.sh` 完成（从认领的任务 JSON
中提取 `title` 和 `description`）。Persona 系统提示词注入由 `san --persona <name> -p`
处理。

### 包结构

```
internal/queue/        ← feature 层
  item.go              WorkItem、ItemStatus、状态转换、ID 生成
  queue.go             JSONL 存储，基于 flock 的原子追加
  claim.go             Claimer：claim、release、complete、verify、fail 及超时释放
cmd/san/queue.go       cobra 子命令注册
```

## 完整示例：认证功能生命周期

以下展示在一个真实功能实现过程中，队列项如何流经状态机。同样的示例保存在
[`san-team/state/EXAMPLE.md`](../../../san-team/state/EXAMPLE.md)。

### 1. Leader 创建任务（全部以 pending 开始）

Leader 拆解"实现用户认证功能"并运行：

```bash
san queue add --dir state/ --role dev \
  --title "定义 User 模型和 UserStore 接口" \
  --description "在 internal/core/user.go 中创建 User struct（ID, Username, PasswordHash, CreatedAt）。定义 UserStore 接口，包含 FindByUsername, Create, FindByID。"

san queue add --dir state/ --role dev \
  --title "实现 UserStore（SQLite）" \
  --description "在 internal/feature/userstore/ 中基于 SQLite 实现 UserStore 接口。参数化查询，bcrypt 密码哈希。"

san queue add --dir state/ --role dev \
  --title "实现 JWT token 生成与验证" \
  --description "创建 internal/core/jwt/ 包。GenerateToken 和 ValidateToken，HS256，24 小时过期。"

san queue add --dir state/ --role dev \
  --title "实现 POST /auth/login handler" \
  --description "在 internal/app/authhandler/ 中创建登录 API handler。接收 {username, password}，返回 {token}。"

san queue add --dir state/ --role dev \
  --title "实现 Auth 中间件" \
  --description "创建中间件：提取 Bearer token、验证、注入用户信息到 context。"

san queue add --dir state/ --role dev \
  --title "登录接口加限流" \
  --description "限制 POST /auth/login 每 IP 每分钟 5 次。"

san queue add --dir state/ --role qe \
  --title "验证认证模块" \
  --description "检出所有认证相关 PR。运行 make test、make lint、make layercheck。在 test-integration/auth/ 下添加集成测试。"

san queue add --dir state/ --role release \
  --title "发布 v1.3.0" \
  --description "生成 CHANGELOG，版本号升至 1.3.0，打 tag v1.3.0，生成 release notes。"
```

Leader 写入后的队列：

```
ID        Role     标题                              状态      负责人
a1b2c3d4  dev      定义 User 模型和接口               pending   -
b2c3d4e5  dev      实现 UserStore（SQLite）           pending   -
c3d4e5f6  dev      实现 JWT token 生成                pending   -
d4e5f6a7  dev      实现 POST /auth/login              pending   -
e5f6a7b8  dev      实现 Auth 中间件                   pending   -
f6a7b8c9  dev      登录接口加限流                     pending   -
a7b8c9d0  qe       验证认证模块                        pending   -
b8c9d0e1  release  发布 v1.3.0                        pending   -
```

### 2. Dev 认领并实现

Dev 的 `run.sh` 执行：`san queue claim --dir state/ --role dev --persona dev`

认领是原子的（flock 保护）。同一时间只有一个进程能认领同一任务。

```
认领 Task 1 → status: claimed, assignedTo: dev
  san --persona dev -p ".." → 实现 → PR #123
  san queue complete --id a1b2c3d4 --pr "https://github.com/genai-io/san/pull/123"
  → status: done

认领 Task 2 → status: claimed, assignedTo: dev
  san --persona dev -p ".." → 实现 → PR #124
  san queue complete --id b2c3d4e5 --pr "https://github.com/genai-io/san/pull/124"
  → status: done

... (Task 3-6 同理)
```

### 3. QE 验证

QE 的 `run.sh` 轮询 `role: qe` 的任务。当所有 dev 任务 done 后：

```
$ san queue claim --dir state/ --role qe --persona qe
→ 认领 Task a7b8c9d0（验证认证模块），status: claimed

$ san --persona qe -p "验证认证模块..."
→ 检出 PR #123-#126 → 运行测试 → 添加集成测试 → 通过

$ san queue verify --id a7b8c9d0 --persona qe --result "全部测试通过，已添加集成测试。"
→ status: verified
```

### 4. Release 发布

Release 的 `run.sh` 轮询 `role: release` 的任务：

```
$ san queue claim --dir state/ --role release --persona release
→ 认领 Task b8c9d0e1（发布 v1.3.0），status: claimed

$ san --persona release -p "发布 v1.3.0..."
→ 生成 CHANGELOG → 更新版本号 → git tag v1.3.0

$ san queue complete --id b8c9d0e1 --persona release
→ status: done
```

### 5. 失败重试示例

```
Task f6a7b8c9: "修复 auth 中间件的 nil pointer"
  第 1 次：claimed → agent 失败 → release → pending（retryCount: 1）
  第 2 次：claimed → agent 失败 → release → pending（retryCount: 2）
  第 3 次：claimed → agent 失败 → release → pending（retryCount: 3）
  retryCount >= maxRetries → san queue fail → status: failed

Leader 检测到失败任务，通知管理员介入。
```

### 完成时的队列全景

```bash
$ san queue list --dir state/
```

```
ID        Role     标题                              状态      负责人    PR
a1b2c3d4  dev      定义 User 模型和接口               done      dev       #123
b2c3d4e5  dev      实现 UserStore（SQLite）           done      dev       #124
c3d4e5f6  dev      实现 JWT token 生成                done      dev       #125
d4e5f6a7  dev      实现 POST /auth/login              done      dev       #126
e5f6a7b8  dev      实现 Auth 中间件                   done      dev       #127
f6a7b8c9  dev      登录接口加限流                     done      dev       #128
a7b8c9d0  qe       验证认证模块                        verified  qe        -
b8c9d0e1  release  发布 v1.3.0                        done      release   -

Total: 8 | pending: 0 | claimed: 0 | done: 7 | verified: 1 | failed: 0
```

### 与 run.sh 的交互

完整的 `run.sh` 循环，展示队列和 Agent 如何组合：

```bash
#!/bin/bash
set -euo pipefail
TEAM_DIR="$(cd "$(dirname "$0")" && pwd)"
PERSONA="${1:?usage: $0 <leader|dev|qe|release>}"
CWD="${2:-$(pwd)}"
INTERVAL="${3:-30}"

echo "[san-team:$PERSONA] 每 ${INTERVAL}s 轮询一次, cwd=$CWD"

while true; do
  TASK=$(san queue claim --dir "$TEAM_DIR/state" --role "$PERSONA" --persona "$PERSONA" 2>/dev/null || true)
  if [ -n "$TASK" ]; then
    ID=$(echo "$TASK" | jq -r '.id')
    TITLE=$(echo "$TASK" | jq -r '.title')
    DESC=$(echo "$TASK" | jq -r '.description')
    echo "[san-team:$PERSONA] 认领 $ID: $TITLE"

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

关键设计要点：`run.sh` 是唯一同时接触队列和 Agent 的组件。队列不知道 persona
的存在。Agent 不知道队列的存在。Shell 脚本是组合层。

## 影响

- **正面**：无数据库依赖。队列是单个 JSONL 文件，可以 `cat` 查看、`grep` 搜索、
  Git 恢复。
- **正面**：`san queue` 独立于 san-team 可用。任何多 Agent 编排场景都可以复用。
- **正面**：CLI 接口可脚本化——`san-team` 中的轮询循环只需 12 行 shell 脚本。
- **正面**：清晰的职责分离。`san queue` 处理原子队列操作。`san --persona <name> -p`
  处理带 persona 的 Agent 执行。`run.sh` 负责组合它们。
- **负面**：JSONL 追加模式会无限增长。后续应增加 `san queue compact` 命令来压
  缩（每个 ID 只保留最后一行）。
- **负面**：基于文件的锁意味着所有队列操作必须在同一文件系统上。跨机器协调需要
  共享文件系统（NFS）或通过 SSH 使用 shell 回退方案。

## 参考资料

- [ADR-0002](0002-autonomous-dev-management.md) — 自主开发管理团队
- [`san-team/DESIGN.md`](../../../san-team/DESIGN.md) — Prompt 优先的 san-team 设计
- [`san-team/state/EXAMPLE.md`](../../../san-team/state/EXAMPLE.md) — 队列示例
