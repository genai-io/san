# PROP-0005：会话管理与文件持久化

[English](proposal-0005-session-management.md)

## 状态与范围

草案 — 2026-11-08。在 sdk-go 的 JSONL 存储之上，定义项目、会话及其事件的目标模型。
存储保持文件形式；SQLite 不在范围内（见[备选方案](#备选方案)）。

| 部分 | 状态 |
| --- | --- |
| [SDK #105](https://github.com/genai-io/sdk-go/pull/105) | 已合并：提交批次（`Writer.Commit`、`Store.Batches`）。 |
| [#737](https://github.com/genai-io/san/pull/737) | 已合并：项目/worktree 身份、检出本地配置、私有 writer 后的类型化事件，采用上一版草案的 18 类模型。在任何 runtime 切换前收敛到本设计。runtime 仍写 schema 4。 |
| [#738](https://github.com/genai-io/san/pull/738) | 已合并：core 中可选的 SDK 准入边界。 |
| runtime 切换、生命周期（回收站/保留） | 未开始；规格见下文。切换是破坏性变更（见[兼容性](#兼容性)）。 |

## 模型

```text
Project  (每个 Git 仓库一个；Git 之外每个目录一个)
  ├── Session  (可恢复的对话；workspace = 它运行的检出目录)
  │     ├── events     只追加、权威、单 writer
  │     ├── attrs      用户意图：改过的标题、归档、固定（可选）
  │     └── blobs      事件引用的内容（图片、大型工具输出、独立调用的请求）
  └── cache            只含派生摘要；随时可删
```

整个设计由四条不变量支撑：

1. **事件是对话的唯一事实来源。** 消息、上下文、设置覆盖和审计都是它的投影，
   不存在并行的 messages/state 存储。
2. **每个会话跨进程只有一个 writer。** 读取永远不需要 writer。
3. **事实先于副作用。** 任何先于或授权副作用（模型调用、工具执行）的事实，
   都在副作用开始前提交。回放只做投影，从不调用模型、工具或 hook。
4. **派生数据皆可丢弃。** 摘要、索引、分页都可删除并从事件无损重建。

会话是一段对话而不是一个进程：`Close` 释放句柄，会话仍可恢复。没有“会话结束”事件。

## 项目身份与配置

| 启动位置 | 项目键 | Workspace |
| --- | --- | --- |
| Git 内 | 规范化的绝对 Git common directory。主检出、linked worktree 及子目录共享一个项目；独立 clone 和 submodule 各自独立。 | 检出根（`git rev-parse --show-toplevel`） |
| Git 外 | 精确的规范化启动目录；不向上查找祖先。 | 同一目录 |

路径取绝对路径、解析符号链接、保留大小写，与信任检查使用同一套规范化。项目目录为
`~/.san/projects/<hash(kind + key)>/`，其中 `project.json` 记录键和显示名；它只是描述信息，
不是身份注册表。因此仓库移动后得到新项目；关联旧项目须显式命令，绝不自动进行。

每个会话在创建时固定 `project`、`workspace` 和 `initial_cwd`。当前工作目录是会话状态
（`execution_cwd`）；改变它会按新位置刷新工具、设置和信任，但绝不把会话移到别的项目。

配置不进入会话存储：

- 项目设置和指令从**当前检出**加载，因此跟随其分支。用户全局设置自然共享。
  没有共享项目根，也不回退到主检出。
- 现有加载器、优先级（user → project → local → persona）和来源追踪不变
  （[配置](../reference/configuration.md)）。
- 会话只保存自身的覆盖（模型、模式……）。恢复时按当前设置和凭据重新解析；
  权限授予永不从历史恢复。已不存在的模型是错误，由用户显式解决。

## 存储布局

```text
~/.san/projects/<project>/
  project.json                     键、显示名
  sessions/<sid>/                  一个会话的全部权威数据
    events.jsonl                   事件（sdk-go 批次模式）
    events.jsonl.lock              writer 锁（sdk-go）
    attrs.json                     用户意图（不存在即默认值）
    blobs/                         按 sha256 内容寻址
  cache/<sid>.json                 列表行摘要（派生）
  trash/<sid>/                     整个移入的已删除会话目录
```

目录按“丢失后果”分组：`sessions/` 与 `trash/` 是权威数据，`cache/` 可丢弃。一个会话就是一个目录，
因此删除、恢复、分叉、备份都只处理一个单元，目录重命名是原子的。父子会话在 `sessions/` 中平级；
关联记录在子会话的 `session.create` 中。以 `.` 开头的名字是暂存，发现时忽略。

## 事件

### 信封

每个事件是一条 sdk-go 应用记录。一起写入的一组事件是一个 sdk-go 批次：
单行、带校验和、链接到前一个尾部的 JSONL，读者要么看到全部，要么一条也看不到。

| 字段 | 含义 |
| --- | --- |
| `seq` | 事件在会话中的位置，从 1 开始。writer 依据当前尾部分配；它就是事件的身份。 |
| `type` | `<object>.<action>`，取自下表。 |
| `v` | 该类型的载荷 schema 版本。 |
| `time` | UTC Unix 毫秒。仅供参考；顺序以 `seq` 为准。 |
| `source?` | 本事件派生自的事件：同会话为 `{seq}`，其他会话为 `{session, seq}`。仅表示溯源。 |
| `payload` | 对应 `type` 的类型化 JSON。 |

事件以位置而非分配的 ID 标识。已提交前缀不可变，因此 `(session, seq)` 在会话生命周期内稳定；
复制前缀的分叉无需改动即可保持所有会话内引用有效。重试幂等由批次提供：重试的提交复用批次 ID，
sdk-go 将相同 ID 且内容相同视为已提交，内容不同视为冲突。

### 词汇

每个类型都是 `<object>.<action>`。object 表示事件关于什么；每个 action 只有一种含义：

| Action | 含义 |
| --- | --- |
| `create` | 新身份。每个会话一次，恒为 `seq 1`。 |
| `update` | 修改具名的值：会话字段用补丁（缺省 / 设置 / 清除），目录用按名的 `set` / `remove`。 |
| `append` | 追加到对话末尾。 |
| `compact` | 用摘要替换活动上下文。历史保留全部内容。 |
| `start` / `finish` | 有界操作。`finish` 通过 `source` 指向它的 `start`，并带上结局。只有当没有其他事件已标记操作开始时，才有持久的 `start`。 |
| `decide` | 对守护某个副作用的关卡作出裁决。 |
| `add` | 仅供展示的记录。 |

### 事件类型

| 分组 | 事件 | 载荷 | 投影效果 |
| --- | --- | --- | --- |
| 身份 | `session.create` | `project`、`workspace`、`initial_cwd`、`agent_id`、`config`（初始覆盖）；`parent? {session, call_id?}`、`fork? {session, seq}` | 身份与溯源。 |
| | `session.update` | `config?`：provider、model、生成选项、模式、autopilot 策略。`state?`：生成的标题、todos、execution_cwd、autopilot 进度、group hint | `config` 作用于下一次请求；`state` 按字段替换。 |
| 对话 | `message.append` | `messages`：非空的 SDK 原生消息列表 | 追加到历史和活动上下文。 |
| | `context.compact` | `summary`：原生消息；`through`：seq | 活动上下文变为 `[summary]` + `through` 之后追加的消息。摘要加入历史。 |
| 模型调用 | `inference.start` | `purpose`、`turn_id?`、`request`（provider、model、选项）、`input`（`conversation {append?}` 或 `standalone`）、`instructions_digest?`、`toolset_digest?` | 打开一次模型调用；每次重试都是独立调用。见[模型调用](#模型调用)。 |
| | `inference.finish` | `outcome`、`stop_reason?`、`usage?`、`duration_ms?`、`error? {message, retryable}` | 关闭 `source` 指向的调用。 |
| 工具调用 | `permission.decide` | `call_id`、`decision`（allow/deny/cancelled）、`by`（config/mode/grant/user/hook/reviewer）、`scope?`、`reason?`、`asked? {options, detail, at}` | 关卡结果。`allow` 之后工具才可能运行。`grant` 表示本会话早先的授权；授权在恢复时绝不带回，这类调用会重新询问。 |
| | `tool.finish` | `call_id`、`outcome`、`duration_ms?`、`error?`、`details?`（文件变更、bash、agent）、`child_session?` | 已运行调用的结局与展示细节。结果正文在工具结果消息中。未通过关卡的调用只有裁决和一条错误工具结果消息。 |
| 模型可见内容 | `instructions.update` | `set {name: {content, slot}}`、`remove [name]`、`cause` | 从此处起的系统提示各节。 |
| | `toolset.update` | `set {name: 原生工具 schema}`、`remove [name]`、`cause` | 从此处起提供给模型的工具。 |
| | `skills.update` | `set {name: disabled/enabled/active}`、`remove [name]`、`cause` | 从此处起的技能可用状态。 |
| 审计与展示 | `hook.finish` | `hook`、`trigger`、`matcher?`、`outcome`（ran/blocked/error）、`duration_ms?`、`reason?` | 一次已完成 hook 运行的记录。 |
| | `annotation.add` | `text`、`by_agent?`、`after_message?` | 展示给人、绝不给模型的备注。默认放在自身位置，设置 `after_message` 时放在该消息之后。 |

载荷规则：

- 原生消息保留 SDK 类型及所有 provider 字段（签名、加密推理、内容顺序、工具 ID）。
  消息 ID 在会话内唯一。
- 补丁区分缺省、设置、清除；`false`、`0`、`""` 是值。目录更新中每个条目在 `set` 与 `remove`
  中只出现一次。拒绝未知字段。空补丁和未变化的目录不写入。
- `cause` 说明谁改变了目录：`boot`、`mcp:<server>`、`plugin:<name>`、`user:<command>`。
- `outcome` 为 `success`、`failure` 或 `cancelled`；失败带有界错误，取消带原因。
- 从不存储：秘密、token 增量、进度刻度、心跳、重复快照，以及可推导的内容（预览、最后提示）。
- 未知的 `type`，或比当前程序更新的 `v`，会阻止恢复和写入。会话仍可读取展示，这类事件以不透明形式显示。

### 模型调用

San 记录自己发起的每次模型调用，因此用量和成本完整，每个请求都能重建。一次调用是 `inference.start`
加 `inference.finish`。调用结果写进拥有它的事件，该事件以 `source` 指向这次调用。

要重建请求，事件必须说明模型收到了什么。调用分两类：

| 类别 | 模型收到的内容 | `inference.start` 记录什么 | 例子 |
| --- | --- | --- | --- |
| 对话调用 | 本会话此刻的对话本身（上下文、系统提示、工具），再加几条只为这次调用追加的消息 | `input.conversation {append?}`：只记追加的消息。其余内容已在事件中，由 `instructions_digest` 和 `toolset_digest` 校验。 | 普通一轮不追加；`/btw` 追加那个问题。 |
| 独立调用 | 单独构建的请求，有自己的提示和输入 | `input.standalone`：请求精确副本的摘要，副本在 `blobs/` 中只存一次 | reviewer 收到的是它自己的提示加待审的工具调用。 |

边界是机械可判定的。只有同时满足以下三条，才是对话调用：

1. 其消息以本会话按投影得到的上下文原样开头，为这次调用添加的内容只能接在后面，不能插入或修改；
2. 其系统提示和工具与投影出的 instructions、toolset 一致，在提交 `inference.start` 时用摘要值校验；
3. 它只经过与普通一轮相同的请求时适配（工具配对修复、provider 适配），没有别的处理。

其他情况都是独立调用：由对话生成的摘要文本、不同的提示、工具子集，或尚未记录的上下文。provider、model
和选项从不决定类别；两类都在 `request` 中记录它们。类别由请求决定，而不是由用途决定：下表列出各用途的
通常类别，提交时摘要值不一致的调用按独立调用记录。例如，runtime 启动前发起的旁路调用会自己构建系统提示，
因此是独立调用。

对话调用频繁且携带整段对话，若每次都复制，等于每次调用都把对话再存一遍；引用事件则只存一次。
独立调用小或少见，其形状属于构建它的代码，只有存精确副本，才能在那段代码改变后仍能重建。

对话调用在同一临界区内读取对话并提交其 `inference.start`，因此中间不会插入任何消息。旁路调用发送工具
只是为了命中提示缓存，工具从不运行。

| 用途 | 通常类别 | 追加的消息 | 结果记录为 |
| --- | --- | --- | --- |
| `turn` | 对话 | 无 | `message.append` |
| `btw`（旁路提问） | 对话 | 进行中工作的占位、问题 | `annotation.add` |
| `recap` | 对话 | 占位、recap 提示 | `annotation.add` |
| `suggest` | 对话 | 占位、建议提示 | 不记录；被采纳的文本之后作为用户输入到达 |
| `compaction` | 独立 | — | `context.compact` |
| `reviewer` | 独立 | — | `permission.decide {by: reviewer}` |
| `hook`（prompt hook） | 独立 | — | `hook.finish` |
| `autopilot` | 独立 | — | `session.update`（进度）或 `message.append`（下一条指令） |
| `group`（解释 `/group`） | 独立 | — | `session.update`（group hint）或 `annotation.add` |

新用途上线前须声明其类别和结果事件；默认是独立调用。自带循环的 agent（子 agent、自学习复审）记录在各自的会话中。

### 提交规则

1. **副作用跟随事实。** 携带工具调用的 assistant 消息在任何工具运行前提交；`inference.start`
   在调用模型前提交；`permission.decide allow` 在工具运行前提交。每个工具调用都经过关卡，
   包括自动允许。提交失败则副作用不开始。
2. **结果与其结局一起提交。** 工具结果消息与其 `tool.finish`、生成的消息与 `inference.finish`
   各自同批。自动裁决可以与 assistant 消息同批。
3. **未完成可见，从不虚构。** 恢复时：
   - 没有裁决的调用从未运行；
   - 已允许但没有 `tool.finish` 的调用结局未知；
   - 没有 finish 的 `inference.start` 被中断；流式输出的部分内容从不提交。

   都不会自动重试或补全。
4. **不明确的提交靠重读解决。** 出错后 writer 重读尾部；批次 ID 存在即已提交。

### 上下文操作

活动上下文只通过 `message.append` 和 `context.compact` 改变。其他看似改变上下文的操作都由别处覆盖：

| 操作 | 记录方式 |
| --- | --- |
| 用户输入、模型输出、工具结果、来自其他会话的消息 | `message.append` |
| 轮次或压缩进行中排队的输入 | 送达前不记录；送达后为 `message.append`。 |
| 压缩：自动、手动 `/compact`、prompt-too-long 恢复 | `context.compact`，及其 `inference.start` / `finish` |
| `/clear` | 新会话（`Create`）。旧会话保持完整、可恢复。不设 `context.clear`：一个会话只承载一段对话。 |
| 回退或编辑之前的消息（目前未实现） | 在该消息之前的 `seq` 处 `Fork`，再在分叉中 `message.append`。源会话从不被改写，因此不需要 `context.rewind`。 |
| 中断的轮次留下没有结果的工具调用 | 不记录。上下文保留它们；每次构建请求时修复工具配对（`ai.Repair`）。 |
| 恢复、分叉或子 agent 续跑时把上下文加载进 runtime | 不记录。runtime 接收投影；与投影相同的替换不是事件。 |
| 子 agent 模型回退 | `session.update.config`；上下文不变。 |
| 请求的 provider 适配（如纯文本模型的图片处理） | 不记录。它是上下文与请求配置的纯函数。 |

### 投影

纯投影器把已提交事件折叠为：

- **Context** — 发给模型的活动消息列表。
- **State** — 生效的覆盖、会话状态和三个目录。
- **History** — 曾有过的所有消息及审计和备注，用于展示和检查。压缩前的消息保留在此。
- **Summary** — 缓存在 `cache/` 中的列表行数据。

恢复流程为 `Open → 投影 → 解析当前设置与信任 → 启动 runtime`。TUI、无界面和 ACP 共用；
ACP 在构建服务前用恢复的 `execution_cwd` 校验请求的 cwd。恢复时按当前配置重建目录；
与投影的差异写成 cause 为 `resume` 的 `*.update` 事件。

## 关键流程

### 一轮对话

```text
commit [message.append user]
commit [inference.start {purpose: turn}]                --> call model
commit [message.append assistant(tool calls), inference.finish,
        permission.decide (automatic ones)]
  each asked call:   commit [permission.decide]         --> run tool if allowed
  each allowed call: commit [message.append tool result, tool.finish]
  each denied call:  commit [message.append error tool result]
```

### 子 agent

子 agent 是独立会话；父会话只记录它接纳的内容。

```text
parent: assistant tool call C (Agent), permission.decide allow C
child:  session.create { parent: {parent_sid, C} }   committed before the child runs
        ... its own independent events ...
parent: commit [message.append tool result for C, tool.finish { call_id: C, child_session }]
```

- 续跑（对已有子会话 `SendMessage`）是另一次工具调用，有自己的 `tool.finish { child_session }`。
- 在工具调用之外稍后送达的结果（后台完成、群组消息）是原父会话中的一条 `message.append`，
  其 `source` 指向子会话的内容事件。
- 不经工具调用启动的会话（cron、workflow）只在存在调用时才设置 `parent.call_id`。
- 投递与重试归传输层或群组收件箱；稳定的批次 ID 使重复投递幂等。回放从不重建进程、回调或子会话所有权。

### 分叉

在源会话的 `seq N` 处分叉：

1. 以暂存目录写入 `session.create { fork: {source, N} }`，随后原样复制事件 `2..N`，并复制被引用的 blob。
2. 清空 group hint；把暂存目录重命名到 `sessions/` 以发布。

位置和原生消息 ID 不变，因此无需改写任何引用。分叉相互独立：删除源会话不会破坏它，
复制前缀中未完成的操作保持未完成。

### 压缩

```text
commit [inference.start {compaction, standalone}] capture: context at seq S
generate summary of that context                 (no lock; turns are paused)
commit [context.compact { summary, through: S }, inference.finish]

before: context = m1 .. m40  (all at seq <= S)
after:  context = [summary]            history = m1 .. m40, summary
```

`through` 精确指出摘要覆盖的范围。检查器能显示哪些消息被摘要；`S` 之后追加的内容会留在上下文中
而不会丢失——目前没有这种内容，因为压缩期间输入会排队。摘要需要保留的近期请求写进摘要文本。
失败或取消的压缩只写 `inference.finish`，上下文不变。

## 一致性与恢复

| 关注点 | 机制 |
| --- | --- |
| 原子分组 | 每组一个 sdk-go 批次：单行、带校验和、哈希链接到预期尾部。 |
| 持久性 | `Commit` 在文件 sync 后返回。仅限本地文件系统；不声称目录 sync 或断电认证。 |
| 单 writer | sdk-go 每会话 `flock`，由打开的文件持有，进程死亡即释放。第二个 writer 得到 `ErrBusy`。 |
| 崩溃恢复 | 下一个 writer 截掉未结束的末行。完整但损坏的行是错误，绝不跳过。比当前程序更新的记录类型阻止恢复。 |
| 资源 | blob 先写入并 sync，再写引用它的事件。 |
| Create / Fork | Create 是一个批次，崩溃后要么什么都没有，要么是有效会话。Fork 通过重命名发布。 |
| 缓存 | 摘要记录构建时事件文件的大小和最后一个批次的哈希，两者都一致才有效。追加会改变大小，整体替换会改变哈希；不使用 mtime。缓存以原子替换写入；过期或缺失的缓存会重建，绝不信任。 |
| 事件分页 | 游标 = `(seq, 字节偏移, 该偏移处的尾部哈希)`。分页从偏移处继续读取，代价为 O(页) 而非重放前缀；哈希不符说明事件文件被替换，游标失败。 |

一致备份是在无 writer 活动时复制整个项目目录。

## API

```go
func OpenStore(ctx context.Context, startDir string) (*Store, error)

func (s *Store) Create(ctx context.Context, opts CreateOptions) (*Session, error)
func (s *Store) Open(ctx context.Context, id string) (*Session, error)
func (s *Store) Fork(ctx context.Context, id string, opts ForkOptions) (*Session, error)
func (s *Store) Get(ctx context.Context, id string) (Summary, error)
func (s *Store) List(ctx context.Context, q SessionQuery) (SessionPage, error)
func (s *Store) Events(ctx context.Context, id string, q EventQuery) (EventPage, error)
func (s *Store) SetAttrs(ctx context.Context, id string, patch AttrsPatch) error
func (s *Store) Delete(ctx context.Context, id string) error
func (s *Store) Restore(ctx context.Context, id string) error
func (s *Store) Clean(ctx context.Context, opts CleanOptions) (CleanResult, error)
func (s *Store) Close() error

func (s *Session) ID() string
func (s *Session) Append(ctx context.Context, events ...Event) error // one batch
func (s *Session) Snapshot() Projection                              // in-memory, committed only
func (s *Session) Close() error                                      // drain, release lock
```

- `OpenStore` 从 `startDir` 解析项目；所有 ID 都必须属于它。跨项目分叉从源项目自己的 `Store` 取源。
- `Create`、`Open`、`Fork` 先取 writer 锁再加载。`Open` 保留已存储的 workspace 和 cwd。
- `Get`、`List`、`Events` 从不取 writer 锁。`List` 覆盖项目的所有 workspace，可按 workspace、
  父会话、归档、活动（`recent` | `all`）和可见性（`live` | `trash` | `all`）过滤，
  按 `(last_active, id)` 排序并使用不透明游标。
- `Append` 是对话事件唯一的写入路径；压缩就是 `context.compact` 与 `inference.finish` 一起 `Append`。
  `session.create` 保留给 `Create` 和 `Fork`。
- `SetAttrs` 无需 writer 锁即可修改用户标题、归档和固定，因此选择器可以整理另一进程正打开的会话。
- 错误可区分：不存在、忙碌、已关闭、冲突、无效、不支持的版本、损坏、取消、存储错误。

当前 `Store`（`Create/Open/List/Fork/Delete`、`Append(transcript.Record)`、立即删除）演进为上述接口；
使用方保留窄的本地接口，通用部分移入 sdk-go。

## 生命周期与保留

会话数据有两类，分开存放：

| | `sessions/<sid>/attrs.json` | `cache/<sid>.json` |
| --- | --- | --- |
| 内容 | 用户意图：`title`（改过的）、`archived_at`、`pinned` | 事件在列表中的投影：标题、最后提示、计数、workspace、父会话、最后活动 |
| 能否从事件推导 | 不能 | 能 |
| 丢失后果 | 数据丢失 | 重建前变慢 |
| 写入者 | 任何进程，在短暂的每会话锁下；不需要 writer 锁 | 任何读者，在重建时 |

分开存放意味着缓存 bug 永远不会损坏用户意图，`cache/` 也永远可以删除。大多数会话不会有 attrs 文件；
不存在即默认值。显示的标题取 `attrs.title`，没有则取事件中生成的 `state.title`，因此分叉会继承生成的标题。
最后活动时间取最后一条 `message.append` 的 `time`。目录、状态、备注事件，以及打开、读取、扫描和恢复都不计入。

| 策略 | 默认 |
| --- | --- |
| 近期列表 | 显示最近 30 天有活动的会话；仅为展示过滤。固定的会话始终显示。 |
| 归档 | 从默认列表隐藏；完全可读、可恢复。 |
| 固定 | 在取消固定前阻止 Delete 和自动保留。 |
| 删除 | 移入回收站，保留 7 天后清除。 |
| 自动保留 | 关闭。启用后（如 90 天不活动）将符合条件的会话移入回收站；从不直接清除。 |

```text
live/archived --Delete / retention--> trash --7 days--> purged
      ^                                 |
      +-------------- Restore ----------+
```

- **Delete** 需要 writer 锁（打开中的会话不能删除），且会话未固定。先在会话目录中写 `deleted.json`（`deleted_at`），
  再一步把目录重命名为 `trash/<sid>/`，并删除缓存的摘要。子会话和分叉是独立会话，不受影响。
  崩溃后会话要么在线要么在回收站，绝不会被拆开。
- **Restore** 把目录重命名回去。若该 ID 已有在线会话则失败；绝不覆盖或改名。
- **Purge** 把超过期限的目录重命名为 `trash/.purging-<sid>/` 再删除；遗留的 `.purging-` 目录在下次运行时完成清理。
- **Clean** 在 UI 可交互后运行保留和清除，每次运行有上限。`DryRun` 只报告将发生什么，不写入。
  自动保留默认只作用于当前 workspace，除非配置为项目级。
- **GC** 删除超过 24 小时的暂存文件和未被引用的 blob。无法确定归属的一律保留。

读取回收站中的会话需要 `visibility=trash`；`Open`、`Fork`、`Append` 在恢复前拒绝它们。

## 兼容性

这是有意为之的破坏性变更：保留旧格式需要可能虚构或丢失事实的映射规则、一致性门禁和第二套读取器，
上文每条规则也都得加上“除非是旧格式”。切换之后 San 只读写上述布局。早期版本写下的会话
（`transcripts/`、`<hash(cwd)>` 分区）既不读取也不转换，原样留在磁盘上，用户可自行删除。
向前方面，未知的事件 `type` 或更新的 `v` 会阻止恢复（见[事件](#事件)）。

## 交付计划

已合并的代码（#737、#738）实现的是上一版草案。下面每一片都是独立 PR，在最后一片之前 runtime 仍写当前格式，最后一片将其移除。

| # | 分片 | 替换 / 新增 |
| --- | --- | --- |
| 1 | 事件模型 | 18 类、全局事件 ID 与 `source_id` → 13 类、`seq` 身份、`source` 引用、词汇、目录与 `context.compact` 载荷 |
| 2 | 校验与投影器 | 按位置的引用检查；关卡、未完成调用与版本规则；Context / State / History 投影 |
| 3 | 模型调用 | `purpose`、对话/独立输入、请求 blob、提交时校验摘要值 |
| 4 | attrs 与缓存 | `attrs.json`、`cache/`、大小 + 批次哈希有效性、按偏移的事件分页 |
| 5 | 生命周期 | `SetAttrs`、`Delete` / `Restore` / `Clean`、整目录回收站、保留、GC |
| 6 | runtime 切换 | `sessions/` 布局；生产者写事件；以前缀复制分叉；恢复与 ACP 校验；删除 schema-4 记录、投影器、`internal/transcript` 以及 `project.go` 中的旧分区查找 |

第 1–2 片要求移植 #737 的测试，而不是删除。

第 6 片改变的是路径，不只是格式。所有使用旧平铺路径的地方都要迁到会话目录：`Session.TranscriptPath` 及其
对 hook 暴露的路径、`session` 中的发现与列表、检查器（`cmd/san/inspector.go`、`internal/inspector`）以及
`transcript.Store`。每个会话目录作为独立的 sdk-go store 打开，固定 ID 为 `events`，因此 sdk-go 的批次格式、
writer 锁和恢复逻辑都不变（已验证：每个目录一个 writer，关闭后目录可直接重命名）。`sessions/`、`trash/`、`cache/`
必须在同一文件系统上，这是重命名原子性的前提。

代码布局遵循同样的划分。`internal/session` 负责产品层 Store 与 Session；其类型化事件代码按职责分文件
（`event_model`、`event_validation`、`event_projector`、`event_storage`、`event_query`），而不是新建包，
因为事件模型在 `session` 之外没有使用方。`internal/transcript` 在第 6 片中删除，其仍被新模型需要的部分（摘要、恢复）并入 `session`。通用部分（批次封装、writer 锁）留在 sdk-go。

## 备选方案

- **全局唯一事件 ID**（上一版草案）。需要分配、跨分区冲突检查，并在分叉时改写所有本地引用。
  位置身份 `(session, seq)` 加批次 ID 幂等，提供同样保证而没有这些成本。
- **`tool.start`、`delegation.start/finish`、`permission.request`**（上一版草案）。每个都重复了别的事件
  已确定的事实：assistant 消息记录了调用，`permission.decide allow` 标记了它可以运行的时刻，子会话的
  `session.create` 与 `tool.finish.child_session` 记录了委派，`permission.decide.asked` 保留了展示给人的内容。
  它们覆盖的每个场景都对应到上面的提交规则。
- **每次调用都存请求精确副本。** 精确，但每一轮的副本都重复整段对话（存储平方增长），且丢失目录何时、
  为何变化。只有独立调用才存副本。
- **只记录主循环调用**（当前行为）。用量和成本会漏掉压缩、reviewer、hook 和旁路调用，其结果也没有溯源。
- **归档与固定作为事件。** 这会要求持有 writer 锁才能整理在别处打开的会话；它们也不是对话事实。
- **通用的 `context.replace`，或 `context.clear` / `context.rewind`。** San 唯一的原地改写是压缩；
  `/clear` 新建会话，回退或编辑更适合用保持源会话完整的分叉来表达（见[上下文操作](#上下文操作)）。
  一个具体的事件更容易校验和阅读。
- **attrs 与摘要缓存合为一个文件。** 重建缓存会重写用户意图，“删缓存永远安全”不再成立。
- **以 `last_resumed_at` 作为活动。** 只打开不操作不算活动；最后事件时间就够了，无需额外的权威字段。
- **随机项目 ID + 注册表。** 注册表需要加锁更新与对账，仓库移动后仍需显式关联。派生键两者都不需要。
- **SQLite。** 2026-11-07 与 JSONL 对比测量（Apple M5，双方均持久 sync）：追加相同（≈4 ms），
  回放 1,000 条消息慢约 20%，列出 100 个会话在缓存热时快 3.5 倍、内存缓存丢失后慢 25 倍
  （JSONL 有持久化摘要），二进制增大 3.8 MB。不值得引入第二个存储引擎；只有在优化摘要缓存和
  回放分配之后、出现新证据时再考虑。
- **读取、转换或双写旧格式。** 转换要映射缺少 start、链接或结局的记录，要么虚构事实要么被阻止；双写使故障模式翻倍；
  sdk-go 也禁止在同一文件中混用两种记录。不背负它，每条规则才能无条件成立。

## 验证

- 身份：worktree 共享项目；clone、submodule 和非 Git 子目录不共享；配置保持检出本地。
- 事件：每种事件类型往返无损；原生 provider 字段保留；回放无副作用；批次重试幂等、冲突失败。
- 在每次 write/sync/rename 处注入崩溃：部分批次、分叉中途、删除中途、恢复中途，均恢复到已提交状态。
- 压缩：`through` 边界、连续压缩、失败时上下文不变。
- 模型调用：每种用途都能重建精确请求（对话调用由事件重建且与摘要值一致，独立调用取自副本）；用量汇总与 provider 一致；恢复重新加载和请求时修复不写事件。
- 关卡：每个工具调用恰有一个裁决；被拒绝的调用有错误结果、没有 `tool.finish`；会话授权不会恢复。
- 版本：未知类型或更新的 `v` 阻止恢复但仍可展示。
- 中断的工具：无裁决 → 从未运行；已允许无 finish → 结局未知；都不重试。
- 删除源后分叉仍独立；子 agent 延迟结果。
- 生命周期：固定与忙碌保护、回收站期限、保留默认关闭、GC 保留未知文件。
- 切换：不再保留 schema-4 读取器；旧项目目录被忽略，而不是被误读。

## 未决问题

- 载荷的精确 Go 类型，以及哪些部分（信封、投影器、批次辅助）属于 sdk-go。
- 仓库移动后的 `relink` 交互。
- 在测量大项目列表性能后，摘要缓存是否应改为每项目一个索引。
