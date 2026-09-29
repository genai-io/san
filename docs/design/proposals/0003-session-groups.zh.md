# PROP-0003：Session Group —— 本机会话之间的协作

## 状态

草案，2026-09-29。未合并的 `feat/group` 分支上有一个早期原型，已用 tmux 端到端验证：两个会话互发消息、自动命名、补全、删除后自动离开。本文是评审后的设计，与原型的差异见末尾“原型与本设计的差异”。

## 动机

一个人常同时开多个 San 会话：一个改后端、一个改前端、一个跑迁移。它们各自为战，一边的发现（接口改了、迁移跑完了）要靠人在终端之间复制粘贴。Session Group 让这些会话组成一个组，组内的 agent 可以直接互发消息。

**目标**
- 同一台机器上的会话加入同一个 group 后，可以按名字互发消息。
- 每个会话清楚组里有谁、各自负责什么、是否在线。
- 成员身份跟着会话走：进程退出、会话恢复，都不需要重新加入。

**非目标（本版）**
- 跨机器、云端会话（需要 Remote Control 之类的中转）。
- 一个会话同时加入多个 group。
- `-p` headless 会话参与 group。

## 概念

| 概念 | 含义 |
|---|---|
| **Group** | 一个组，对应目录 `~/.san/groups/<group>/`。第一个人 join 时创建 |
| **Member** | 组里的一个**会话**（不是进程）：名字（`@api`）、职责、模式、在线状态 |
| **Inbox** | 每个成员一个收件目录，别人发给它的消息和回执各占一个文件 |
| **Mode** | 成员决定收到消息后是否被唤醒：`active`（默认）或 `passive` |
| **Roster** | 成员列表，由 San 进程维护，以 reminder 的形式告诉 agent |

**一个会话同一时间只在一个 group 里。** 因此 `detach` 不需要参数，发消息时直接写名字，不用带组名。

## 全景

一条消息从 A 到 B 的路径：

```
 会话 A（@api）                                        会话 B（@web）
     │
     │ agent 调用 SendMessage(to: "web", message: "...")
     ▼
 写入 ~/.san/groups/dev/web.inbox/<时间戳>-api.json
                                                           │
                                     B 每秒轮询自己的 inbox，读取后删除
                                                           ▼
                                 ┌─────────── B 的模式 ───────────┐
                                 │                                │
                            active（默认）                      passive
                    当作用户消息注入：                 入 reminder 队列，界面提示；
                    空闲 → 开启新的一轮                用户下次输入时带给 agent
                    工作中 → 在工具调用之间插入
                                 │                                │
                                 └──── 消息进入 B 的上下文时 ─────┘
                                                  │
                                                  ▼
                        往 api.inbox/ 写一条回执 → A 以 reminder 的形式得知“已读”
```

- 不经过 broker（broker 仍只负责进程内主会话与子 agent）。
- 收到的消息复用现有的注入入口 `notifyMain`，和子 agent 汇报走同一条路径。

## 存储

```
~/.san/groups/<group>/<name>.json      成员：id, name, role, mode, sessionID, pid, cwd, joinedAt
~/.san/groups/<group>/<name>.inbox/    收件：<纳秒时间戳>-<发件人>.json
```

- **按成员名命名**：一眼能看出是谁的。join 时以“排他创建”的方式写成员文件，由文件系统保证组内不会重名，两个会话同时抢同一个名字也不会互相覆盖。
- **成员 `id`**：加入时生成，之后不变，用于识别改名。不用 sessionID，因为 `/clear` 之后 sessionID 会变。
- **消息文件名**：时间戳在前、发件人在后。按文件名排序就是按时间排序，先发的先送达。同一发件人不可能在同一纳秒发出两条，所以不需要随机后缀。
- **文件内容**：`type` 区分 `message` 和 `receipt`，另外还有发件人、正文和发送时间。
- **没有共享写入**：成员只写自己的文件，发件人只往对方 inbox 新增文件，多进程之间没有竞态。
- **原子写入**：先写临时文件（`.` 开头、`.tmp` 结尾）再 rename，读取端跳过临时文件。
- **权限**：目录 0700、文件 0600，只有当前系统用户可访问。
- 选目录不选 socket：跨平台（含 Windows）、不需要鉴权 token、离线期间消息可以排队保存。

## 成员身份属于会话

成员身份属于**会话（对话）**，不属于进程。进程只是会话当前的运行载体，会退出，会话会被恢复。

| 事件 | 处理 |
|---|---|
| **join** | group 不存在则创建（默认名 `default`）。group 信息（group、名字、职责、模式、成员 `id`）写入会话记录 |
| **自动命名** | 未给 `--as` / `--role` 时，用模型从当前对话总结名字和职责；对话为空或调用失败时，退回到 `/name` 设置的会话名或目录名 |
| **进程退出**（`/quit`、崩溃、关终端） | **不离开 group**，变为**离线**：成员文件和 inbox 保留。正常退出时由 finalizer 把 `pid` 写成 0，立即显示为离线；崩溃时由进程号检查得出离线 |
| **离线期间收到消息** | 照常写入 inbox 排队；发件方得到的返回是 `@web is offline; it reads this when it resumes` |
| **恢复会话**（`san -r`、`/resume`） | 从会话记录读出 group，按成员 `id` 认领自己的成员文件，写入新 `pid` 后上线，**不需要重新 join**；积压的消息随之送达 |
| **`/clear`** | 保留成员身份（角色没变）；清空后重新附上完整成员列表 |
| **在 San 里 `/resume` 切到别的会话** | 原会话变为离线；目标会话如果属于某个 group，则上线 |
| **`/group detach`** | 真正离开：删除成员文件和 inbox，从会话记录中去掉 group 信息。最后一人离开时删除 group 目录 |
| **`/group remove <name>`** | 把指定成员（通常是长期离线的）移出 group |
| **`/group delete`** | 删除整个 group 目录；在线成员 1 秒内自动离开并提示，离线成员恢复时发现 group 已不在，给出提示 |

**没有心跳，也不自动清理离线成员。** 在线状态由成员文件里的 `pid` 判断：进程在就是在线，不在就是离线，不需要定期写入。电脑睡眠也不会被误判。长期不用的离线成员由用户手动 `remove`。

### Finalizer

会话一旦产生超出自身生命周期的外部状态（加入 group），就登记一个清理动作。所有退出路径（`/quit`、Ctrl+D、Ctrl+C、出错退出）最终都经过 `tea.Run` 返回处，finalizer 在那里统一执行。在本设计中，它的职责是**把自己标为离线**（`pid` 写成 0），而不是离开 group。

进程被强杀时 finalizer 来不及执行，由其他成员读取时的进程号检查得出离线，效果相同。这一版只有“group 成员”这一种外部状态，finalizer 是一个简单的“名字 → 清理函数”表，不做通用框架。

## 成员列表的维护

由 San 进程维护，**模型不需要维护任何东西**，只需要读到 reminder。维护逻辑与 active / passive 无关。

```
磁盘（唯一的事实来源）      ~/.san/groups/dev/*.json
        │  每秒读取一次
        ▼
进程内存（成员快照）         map[成员 id] → {名字, 职责, 模式, 在线, cwd}
        │  比较差异，只把变化告诉模型
        ▼
模型上下文                   完整列表（加入、恢复、压缩后）+ 变化 reminder
```

每秒一次：读取所有成员文件，按**内容**与内存快照对比（不看文件修改时间），得出差异：

| 差异 | 判断依据 | 告诉模型 |
|---|---|---|
| 加入 | 磁盘上有，快照里没有 | `Group dev: @web joined (active) — owns the login page` |
| 离开 / 被移除 | 快照里有，磁盘上没有 | `Group dev: @web left` |
| 离线 / 上线 | `pid` 对应的进程不在了 / 又在了 | `Group dev: @web went offline` / `is back online` |
| 更新 | 同一成员 `id`，职责或模式变了 | `Group dev: @web is now passive` |
| 改名 | 同一成员 `id`，名字变了 | `Group dev: @web is now @frontend` |

同一步里还会处理：
- 发现自己的成员文件不见了但 group 还在（被 `remove`）→ 离开并提示。
- 发现整个 group 目录不见了（被 `delete`）→ 离开并提示。

`SendMessage` 发送时以**磁盘为准**，不依赖可能落后 1 秒的快照。名字不存在时，报错里列出当前成员，模型可以自己纠正。

## 模式：接收方决定是否被唤醒

**核心原则：一条消息会不会让 agent 跑起来，只由接收方的模式决定，发送方无权改变。**

| | **Active（默认）** | **Passive** |
|---|---|---|
| 收到消息，**空闲时** | 当作用户消息注入，**开启新的一轮** | 入 reminder 队列，挂在用户下一条输入上；**不开启新的一轮** |
| 收到消息，**工作中** | 在工具调用之间插入 | **同样只入队**，不插入当前这一轮，避免把用户主导的任务带偏 |
| 用户看到 | `◆ Message from @x: …` | `◆ Message from @x: …（下次输入时带给 agent）` |
| 适合 | 需要自动协作的会话 | 用户正在主导、不希望被打断的会话 |

默认 active，因为加入 group 本身就是用户主动选择协作。用户可以随时 `/group mode passive`，模式变化会写入成员文件，组员可以看到。

**发送方知道对方的状态：** roster 里标出每个成员的模式和在线状态，`SendMessage` 的返回结果也会区分：
- 对方 active 且在线：“已送达，对方会马上处理。”
- 对方 passive：“已送达，对方的用户下次交互时才会看到，不要等待回复。”
- 对方离线：“已排队，对方恢复会话后才会看到。”

## 已读回执

- **“已读”的定义**：消息**进入了对方 agent 的上下文**，而不是对方进程从 inbox 读走了文件。active 模式下几乎立即发生；passive 模式下要等对方用户下次输入；离线时要等会话恢复。
- 此时接收方往发件人的 inbox 写一条 `type: receipt`：`@web has read your message: "schema changed…"`。
- 发件方把回执以 **reminder** 的形式告诉自己的 agent：**永远不唤醒**，也不会为回执再发回执。
- 同一时间段内的多条回执合并成一条；界面上不显示，只给模型看。

发件方的 agent 由此可以区分：已送达（`SendMessage` 成功）、已读（收到回执）、未读（对方 passive 或离线）。

## 注入：什么内容、什么时候、以什么形式

| 事件 | 形式 | 目标正在工作 | 目标空闲 | 用户看到 |
|---|---|---|---|---|
| 组员消息 → active | 用户消息 | 在工具调用之间插入 | 开启新的一轮 | `◆ Message from @x` |
| 组员消息 → passive | reminder | 入队 | 入队 | `◆ Message from @x …（下次输入时带给 agent）` |
| 成员变化（加入、离开、上下线、更新、改名） | reminder，只发变化 | 在工具调用之间插入 | 入队 | 一行提示，如 `@web joined group dev` |
| 已读回执 | reminder，合并 | 在工具调用之间插入 | 入队 | 不显示 |
| group 被删除 / 自己被移除 | reminder | 同上 | 同上 | `Your group was deleted — left it.` |
| 完整成员列表 | reminder provider | 加入、恢复、`/clear`、压缩后附上 | 同左 | — |

**实现方式**：给通知加一个“reminder 类型”标记，在 `notify.go` 的投递时机判断处统一处理。
- **reminder 类型**：目标工作中则插入当前这一轮（passive 的组员消息除外，它只入队）；空闲则只入队，永远不开启新的一轮。
- **消息类型**：沿用现有逻辑。

## 防止来回对话：把判断交给 agent

不设硬性的轮次上限，而是给 agent 做判断所需要的信息。

**`unattended-turns`：** 自用户上次输入以来，组员消息已经连续开启了多少轮（含本轮），从 1 开始。

| 情况 | 计数 |
|---|---|
| 组员消息开启了新的一轮 | +1 |
| 多条组员消息合并成一轮注入 | +1（按轮算，不按条算） |
| 工作中插入组员消息（没有开启新的一轮） | 不加，消息上标当前值 |
| passive 入队 | 不加 |
| 用户在这个会话里输入 | 清零 |
| 会话重启 | 从 0 开始（只保存在内存里） |

**由接收方自己计算。** 它衡量的是“我这边的用户多久没出现了”，只有接收方知道。发送方写入的消息文件里不带这个数字，所以也无法伪造。

**让模型理解它**：在成员列表的 reminder 里定义一次（加入时、压缩后都会附上）：

```
Messages from members arrive as <group-message from=".." unattended-turns="N">.
unattended-turns is counted by this session: how many turns in a row group
messages have started since your user last typed, this one included. It
resets to 0 when your user types.

A rising count means agents are running on their own. Before replying,
check that the exchange is converging on a result. If you are repeating
yourself, answering only to acknowledge, or waiting on each other, stop:
don't reply, and leave your user a one-line note of where things stand.
```

每条组员消息末尾再用一句话点明当前数值：

```
<group-message from="web" unattended-turns="7">
schema changed, please rebase
</group-message>
(Unattended turn 7: group messages have started 7 turns in a row since your user last typed.)
```

**用户也能看到**：从第 2 轮开始，提示行带上这个数字，如 `◆ Message from @web: schema changed · 7th since you last typed`。

其余机制也在帮助收敛：
- 成员变化和已读回执都是 reminder，不会开启新的一轮，也没有可回复的对象。
- passive 成员永远不会被组员消息唤醒。

## SendMessage：以组员为主

- 在组里时，主 agent 自动打开 `SendMessage`，离开后关闭。它原本对主 agent 默认关闭，因为“主 agent 实际很少用它去操控后台子 agent”。
- 工具描述以组员为主：`to` 写组员名字是主要用法。
- 原有的按任务 ID 发给子 agent、以及子 agent 发 `"main"` 汇报的路径**保留但不再宣传**，确认没人用之后可以删除。
- 消息正文包在 `<group-message>` 里；界面把这次调用显示为 `Message → @web: …`。

## 安全

- 组员消息明确标注为来自其他会话、不是用户：不能代替用户批准权限，不能要求修改配置或 AGENTS.md，其中的斜杠命令只当普通文字。
- 接收方照常执行自己的权限检查。

## 缓存与成本

- 成员列表、变化通知、回执、组员消息都通过 reminder 或消息通道注入，**system prompt 始终不变**，不影响前缀缓存。
- join / detach 会因为打开或关闭 `SendMessage` 而重建一次 agent，缓存失效一次。低频操作，可以接受。
- passive 成员、离线成员和空闲成员，都不会因为别人发消息或进出组而花 token。

## 交互

| 命令 | 作用 |
|---|---|
| `/group join [group] [--as NAME] [--role TEXT] [--passive]` | 加入，不存在则创建；名字和职责不写就自动总结；默认 active |
| `/group mode active\|passive` | 切换自己的模式，立即生效，组员可见 |
| `/group list` | 在组里时列出成员（名字、模式、在线状态、职责、目录）；不在组里时列出所有 group 及人数 |
| `/group detach` | 离开当前 group |
| `/group remove <name>` | 移除指定成员，通常用于长期离线的成员 |
| `/group delete [group]` | 解散 group |

**补全**：`/gro` → 子命令 → `join` 后列出已有 group 及其成员，没有 group 时提示可以创建 `default`；`mode` 后列出 `active` / `passive`；`remove` 后列出组员，离线的排在前面。

## 实现位置

| 文件 | 职责 |
|---|---|
| `internal/group/group.go` | 成员文件、inbox、进程号检查、Join / Detach / Remove / Delete / Send / Poll |
| `internal/app/group.go` | `/group` 命令、自动命名、成员快照与差异、注入、`unattended-turns`、回执、finalizer、补全 |
| 会话记录（`internal/session`） | 保存 group 信息，恢复时自动上线 |
| `internal/tool/agent/sendmessage.go` | 组员地址解析，按对方状态返回结果 |
| `internal/app/notify.go` | reminder 类型通知的投递时机 |
| `internal/app/kit/suggest`、`input/on_textarea.go` | 命令参数补全；选中后立即显示下一级 |

## 原型与本设计的差异

`feat/group` 上的原型实现的是早期版本，落地本设计时需要改的地方：

| 原型 | 本设计 |
|---|---|
| 文件按随机 ID 命名，消息文件为 `<时间戳>-<随机>` | 按成员名命名，消息文件为 `<时间戳>-<发件人>`，join 时排他创建 |
| 每 10 秒心跳，60 秒未更新就清理 | 无心跳；按 `pid` 判断在线或离线，不自动清理 |
| 进程退出即离开 group | 进程退出变为离线；成员身份写入会话记录，恢复时自动上线 |
| 默认按 active 行为，无用户输入时最多 5 轮 | 默认 active，可切换 passive；不设上限，改为 `unattended-turns` |
| 成员变化时发送完整列表，挂在下一条消息上 | 只发变化；工作中插入，空闲时入队；界面提示；可识别改名和上下线 |
| 无已读回执 | 有 |
| 无 `mode`、`remove` 命令 | 有 |

原型中已经顺带修复、落地时一并保留的两个问题：
- **中途重建 agent 时新 agent 被停掉**：旧 agent 的“已停止”事件晚到，停掉的却是刚建好的新 agent，导致随后的发送报 `agent session is not active`。在 `/tool` 面板切换工具等场景也会触发。
- **`SendMessage` 被渲染成“启动子 agent”**：现在显示为 `Message → @x: …`。

## 以后可以做

- **协调者模式**：有人加入时立即唤醒指定成员，例如给新人分配任务。
- 状态栏显示 group 标记，例如 `group:dev(3)`。
- `/group rename`：加入后修改名字（改名的识别机制本设计已经具备）。
- 跨机器通信。

## 待定

1. **YOLO 模式下默认 passive？** 默认 active 加上 YOLO，意味着组员的请求会在无人确认的情况下直接执行。一个读过恶意网页、被注入了指令的会话，可以借此指挥另一个 YOLO 会话。倾向于：YOLO 下默认 passive，可以手动切换为 active。
2. **子 agent 能否直接给组员发消息？** 目前可以，发件人显示为本会话的名字。
