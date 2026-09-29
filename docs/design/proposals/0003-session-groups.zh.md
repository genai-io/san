# PROP-0003：Session Group —— 本机会话之间的协作

## 状态

草案，2026-09-29。核心原型已在未合并的 `feat/group` 分支实现并用 tmux 端到端验证（两个会话互发消息、自动命名、补全、finalizer、删除后自动离开）。本文中标 ⬜ 的部分是讨论中新定、尚未实现的。

## 动机

一个人常同时开多个 San 会话：一个改后端、一个改前端、一个跑迁移。它们各自为战，一边的发现（接口改了、迁移跑完了）要靠人在终端之间复制粘贴。Session Group 让这些会话组成一个组，组内的 agent 可以直接互发消息。

**目标**
- 同一台机器上的会话加入同一个 group 后，可以按名字互发消息。
- 每个会话清楚组里有谁、各自负责什么。
- 默认不打扰：别人的消息不会擅自让你的 agent 跑起来，更不会引发 agent 之间停不下来的来回对话。

**非目标（本版）**
- 跨机器、云端会话（需要 Remote Control 之类的中转）。
- 一个会话同时加入多个 group。
- `-p` headless 会话参与 group。

## 概念

| 概念 | 含义 |
|---|---|
| **Group** | 一个组，对应 `~/.san/groups/<group>/` 目录。第一个人 join 时创建，最后一个人离开时自动消失 |
| **Member** | 组里的一个会话，有名字（`@api`）、职责、工作目录和**模式** |
| **Inbox** | 每个成员一个收件目录，别人发给它的消息各占一个文件 |
| **Mode** | 成员决定自己收到消息后怎么处理：`passive`（默认）或 `active` |
| **Roster** | 组员列表，以 reminder 的形式告诉 agent |

**一个会话同一时间只在一个 group 里**。因此 `detach` 不需要参数，发消息时直接写名字，不用带组名。

## 全景

一条消息从 A 到 B 的路径：

```
 会话 A（@api）                                        会话 B（@web）
     │
     │ agent 调用 SendMessage(to: "web", message: "...")
     ▼
 写入 ~/.san/groups/dev/b2.inbox/<时间戳>-<随机>.json
                                                           │
                                     B 每秒轮询自己的 inbox，读取后删除
                                                           ▼
                                 ┌─────────── B 的模式 ───────────┐
                                 │                                │
                            passive（默认）                     active
                    入 reminder 队列，界面提示；        当作用户消息注入：
                    用户下次输入时带给 agent           空闲 → 开启新的一轮
                                                      工作中 → 在工具调用之间插入
```

同时，每个会话的轮询还负责：
- **心跳**：每 10 秒重写一次自己的成员文件，证明自己还活着。
- **成员变化**：发现有人加入或离开，以 reminder 的形式告诉自己的 agent，并在界面上提示。
- **group 被删除**：发现组目录不在了，自动离开。

- 不经过 broker（broker 仍只负责进程内主会话与子 agent）。
- 收到的消息复用现有的注入入口 `notifyMain`，和子 agent 汇报走同一条路径。

## 存储与并发 ✅

```
~/.san/groups/<group>/<id>.json      成员：name, role, cwd, mode, maxTurns, joinedAt
~/.san/groups/<group>/<id>.inbox/    收件：<纳秒时间戳>-<随机>.json，按文件名即按时间排序
```

- **没有共享写入**：成员只写自己的文件，发件人只往对方 inbox 新增文件，多进程之间没有竞态。
- **原子写入**：先写临时文件（`.` 开头、`.tmp` 结尾）再 rename，读取端跳过临时文件，不会读到半条消息。
- **权限**：目录 0700、文件 0600，只有当前系统用户可访问。
- 选目录不选 socket：跨平台（含 Windows）、不需要鉴权 token、进程重启消息不丢。

## 生命周期

| 事件 | 处理 | |
|---|---|---|
| **join** | group 不存在则创建（默认名 `default`）；名字重复自动加 `-2` | ✅ |
| **自动命名** | 未给 `--as` / `--role` 时，用模型从当前对话总结名字和职责；对话为空或调用失败时，退回到 `/name` 设置的会话名或目录名 | ✅ |
| **detach** | 删除自己的成员文件和 inbox；最后一人离开时删除 group 目录 | ✅ |
| **delete** | 删除整个 group 目录；其他成员 1 秒内发现目录不在，自动离开并提示 | ✅ |
| **正常退出** | 执行 finalizer | ✅ |
| **崩溃 / 被 kill** | 心跳兜底：每 10 秒重写成员文件；超过 60 秒未更新，任何读取者代为清理 | ✅ |
| **睡眠后唤醒** | 心跳重写时如果发现自己已被清理，就重新写回，自动恢复成员身份 | ✅ |

### Finalizer

会话一旦产生超出自身生命周期的外部状态（例如加入 group），就登记一个清理动作。所有退出路径（`/quit`、Ctrl+D、Ctrl+C、出错退出）最终都经过 `tea.Run` 返回处，finalizer 在那里统一执行。

进程被强杀时 finalizer 来不及执行，由其他成员通过心跳检测发现并代为清理，相当于 Kubernetes 中由 controller 完成收尾。这一版只有“group 成员”这一种外部状态，finalizer 是一个简单的“名字 → 清理函数”表，不做通用框架。

## 模式：由接收方决定是否被唤醒

**核心原则：一条消息会不会让 agent 跑起来，只由接收方的模式决定，发送方无权改变。** 否则一个 active 成员就能把所有 passive 成员叫起来，passive 形同虚设。

| | **Passive（默认）** | **Active** |
|---|---|---|
| 收到消息，**空闲时** | 入 reminder 队列，挂在用户下一条输入上；**不开启新的一轮** | 当作用户消息注入，**立即开启新的一轮** |
| 收到消息，**工作中** | **同样只入队**，不插入当前这一轮，避免把用户主导的任务带偏 | 在工具调用之间插入当前这一轮 |
| 用户看到 | `◆ Message from @x: …（下次输入时带给 agent）` | `◆ Message from @x: …` |
| 适合 | 人在主导的会话 | 无人值守、专门响应请求的会话 |

### Active 的连续轮次上限 ⬜

- 只计 **active 成员在用户没有输入期间、由组员消息连续开启的轮数**。
- **用户一输入就清零，重新计算。**
- 上限**可配置**：默认 20，`0` 表示不限。
- 达到上限后，该成员临时按 passive 处理（消息只入队），并提示“已达上限，后续消息等你回来”。用户一输入就清零，恢复 active。

### 发送方要知道对方的模式 ⬜

- roster 里标出每个成员的模式：`@web (passive): 负责登录页`。
- `SendMessage` 的返回结果按对方模式区分：
  - 对方是 active：“已送达，对方会马上处理。”
  - 对方是 passive：“已送达，对方的用户下次交互时才会看到，不要等待回复。”

## 注入：什么内容、什么时候、以什么形式

| 事件 | 形式 | 目标正在工作 | 目标空闲 | 用户看到 | |
|---|---|---|---|---|---|
| 组员消息 → **active** 成员 | 用户消息 | 在工具调用之间插入 | 开启新的一轮（受上限约束） | `◆ Message from @x` | ✅（上限可配置 ⬜） |
| 组员消息 → **passive** 成员 | reminder | 入队 | 入队 | `◆ Message from @x …（下次输入时带给 agent）` | ⬜ |
| 有人加入 / 离开 | reminder，**只发变化** | 在工具调用之间插入 | 入队 | `@web joined group dev — 职责` | ⬜ |
| group 被删除 | reminder | 同上 | 同上 | `Your group was deleted — left it.` | ✅ |
| 完整 roster | reminder provider | 加入时、压缩后附上 | 同左 | — | ✅ |

**注入方式**：给通知加一个“reminder 类型”标记，在 `notify.go` 的投递时机判断处统一处理。
- **reminder 类型**：目标工作中则插入当前这一轮（passive 的组员消息除外，它只入队）；目标空闲则只入队，永远不开启新的一轮。
- **消息类型**：沿用现有逻辑，工作中插入，空闲时开启新的一轮。

成员变化对 active 和 passive **都只走 reminder**，永远不唤醒。active 的 agent 下一次被唤醒时自然能看到。

## SendMessage：以组员为主

- 在组里时，主 agent 自动打开 `SendMessage`，离开后关闭。它原本对主 agent 默认关闭，因为“主 agent 实际很少用它去操控后台子 agent”。
- ⬜ 工具描述改为以组员为主：`to` 写组员名字是主要用法。
- 原有的按任务 ID 发给子 agent、以及子 agent 发 `"main"` 汇报的路径**保留但不再宣传**。确认没人用之后可以删除。
- ⬜ 发给不存在的名字时，报错里列出当前在线的成员，模型可以自己纠正。
- 消息正文包在 `<group-message group=".." from="..">` 里；界面显示为 `Message → @web: …`。

## 防止来回对话

| 组合 | 结果 |
|---|---|
| passive ↔ passive | 永远不会自动开启新的一轮 |
| active → passive | 消息只入队，passive 方不会自动回复，没有循环 |
| active ↔ active | 可能循环 → 连续轮次上限兜底 |

另外两层软约束：
- 成员变化走 reminder，不会开启新的一轮，也没有可回复的对象。
- roster 里写明“只在能推进工作时才回复，不要为了确认而回复”。实测中 A 收到 `ping` 后没有回 beta，而是转头问用户接下来做什么。

## 安全 ✅

- 组员消息明确标注为来自其他会话、不是用户：不能代替用户批准权限，不能要求修改配置或 AGENTS.md，其中的斜杠命令只当普通文字。
- 接收方照常执行自己的权限检查。
- 默认 passive，意味着组员的请求默认要经过用户那一轮输入才会被处理。active 等于用户显式选择了让它自主运行。所以不再单独为 YOLO 模式设“先扣下”的规则。

## 缓存与成本

- roster、变化通知、组员消息都通过 reminder 或消息通道注入，**system prompt 始终不变**，不影响前缀缓存。
- join / detach 会因为打开或关闭 `SendMessage` 而重建一次 agent，缓存失效一次。低频操作，可以接受。
- passive 成员和空闲的成员，不会因为别人发消息或进出组而花任何 token。

## 交互

| 命令 | 作用 | |
|---|---|---|
| `/group join [group] [--as NAME] [--role TEXT] [--active] [--max-turns N]` | 加入，不存在则创建；名字和职责不写就自动总结；默认 passive | ✅（`--active`/`--max-turns` ⬜） |
| `/group mode active\|passive [--max-turns N]` | 切换自己的模式，立即生效，并同步到成员文件，组员可见 | ⬜ |
| `/group detach` | 离开当前 group | ✅ |
| `/group list` | 在组里时列出成员（名字、模式、职责、目录）；不在组里时列出所有 group 及人数 | ✅（显示模式 ⬜） |
| `/group delete [group]` | 解散 group，所有成员自动离开 | ✅ |

**补全**：输入 `/gro` → 选中后弹出子命令 → 选 `join` 后列出已有 group 及其成员；没有 group 时提示可以创建 `default`；选 `mode` 后列出 `active` / `passive`（⬜）。

## 实现位置

| 文件 | 职责 |
|---|---|
| `internal/group/group.go` | 成员文件、inbox、心跳、失效清理、Join / Detach / Delete / Send / Poll |
| `internal/app/group.go` | `/group` 命令、自动命名、roster、轮询与注入、轮次计数、finalizer、补全 |
| `internal/tool/agent/sendmessage.go` | 组员地址解析，写入对方 inbox |
| `internal/app/notify.go` | ⬜ reminder 类型通知的投递时机 |
| `internal/app/kit/suggest`、`input/on_textarea.go` | 命令参数补全；选中后立即显示下一级 |

## 这次一并修复的已有问题 ✅

- **中途重建 agent 时新 agent 被停掉**：旧 agent 的“已停止”事件晚到，停掉的却是刚建好的新 agent，导致随后的发送报 `agent session is not active`。在 `/tool` 面板切换工具等场景也会触发。
- **`SendMessage` 被渲染成“启动子 agent”**：现在显示为 `Message → @x: …`。

## 以后可以做

- **协调者模式**：有人加入时立即唤醒指定成员，例如给新人分配任务。可选开关，默认关闭。
- 状态栏显示 group 标记，例如 `group:dev(3)`。
- 加入后修改名字或职责（目前需要 detach 再 join）。
- 跨机器通信。

## 待定

1. active 连续轮次上限默认 20 是否合适？
2. 心跳 10 秒、失效判定 60 秒是否合适？
3. 子 agent 能否直接给组员发消息？目前可以，发件人显示为本会话的名字。
