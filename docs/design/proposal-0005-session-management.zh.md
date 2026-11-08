# PROP-0005：会话管理与文件持久化

[English](proposal-0005-session-management.md)

## 状态与范围

草案 — 2026-11-08。在 sdk-go 的 JSONL 存储之上，定义项目、会话及其事件的目标模型。
存储保持文件形式；SQLite 不在范围内（见[备选方案](#备选方案)）。

| 部分 | 状态 |
| --- | --- |
| [SDK #105](https://github.com/genai-io/sdk-go/pull/105) | 已合并：提交批次（`Writer.Commit`、`Store.Batches`）。 |
| [#737](https://github.com/genai-io/san/pull/737) | 未合并：项目/worktree 身份、检出本地配置、私有 writer 后的类型化事件。runtime 仍写 schema 4。 |
| runtime 切换、生命周期（回收站/保留）、旧格式转换 | 未开始；规格见下文。 |

## 模型

```text
Project  (每个 Git 仓库一个；Git 之外每个目录一个)
  └── Session  (可恢复的对话；workspace = 它运行的检出目录)
        ├── events         只追加、权威、单 writer
        ├── attrs          小型可变文件：标题、归档、固定、活动时间
        ├── blobs          事件引用的内容（图片、大输出、提示词）
        └── summary cache  可重建
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
旧的 `<hash(cwd)>` 分区原地保持可读，不移动、不合并。

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
  transcripts/<sid>.jsonl          事件（sdk-go 批次模式）
  transcripts/<sid>.jsonl.lock     writer 锁（sdk-go）
  transcripts/<sid>.attrs.json     可变属性（权威）
  transcripts/<sid>.jsonl.meta     摘要缓存（可重建）
  blobs/<sid>/                     按 sha256 内容寻址
  trash/<sid>/                     已删除会话：manifest + 移入的文件
  legacy/<sid>.jsonl               转换后保留的 schema-4 原件
```

父子会话在 `transcripts/` 中是平级的；关联记录在子会话的 `session.create` 中。
以 `.` 开头的文件是暂存文件，发现时忽略。

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

### 事件类型

| 事件 | 载荷 | 投影效果 |
| --- | --- | --- |
| `session.create` | `project`、`workspace`、`initial_cwd`、`agent_id`、`config`（初始覆盖）；`parent? {session, call_id}`、`fork? {session, seq}` | 恒为 `seq 1`。身份与溯源。 |
| `message.append` | `messages`：非空的 SDK 原生消息列表 | 追加到历史和活动上下文。 |
| `context.replace` | `reason`（compaction/clear/rewind/edit）；`through`：seq；`entries`：有序列表，每项是已有消息 ID 或新的原生消息 | 活动上下文变为 `entries` + `through` 之后追加的所有消息。新消息（如摘要）加入历史；历史不删除任何内容。 |
| `session.update` | `config?`：模型、provider、生成选项、模式、autopilot 策略；`state?`：todos、execution_cwd、autopilot 进度、group hint、活动技能 | 后续请求使用新覆盖；状态按字段替换。 |
| `inference.start` | `turn_id`、`input_through`（seq）、`request`（provider、model、选项）、`instructions` 与 `tools`（blob 摘要） | 打开一次尝试。输出消息以它为 `source`。 |
| `inference.finish` | `outcome`、`stop_reason?`、`usage?`、`duration_ms?`、`error?` | 关闭 `source` 指向的尝试。 |
| `tool.finish` | `call_id`、`outcome`、`duration_ms?`、`error?`、`details?`、`child_session?` | 单次工具调用的审计与展示细节。结果正文在工具结果消息中。 |
| `permission.decide` | `call_id`、`decision`（allow/deny/cancelled）、`scope`、`by`（config/user/hook/reviewer）、`asked_at?`、`reason?` | 仅审计；回放绝不重新授权。 |
| `hook.finish` | `hook`、`trigger`、`outcome`、`duration_ms?`、`effects?` | 已完成 hook 的审计。异步派发在完成前不记录。 |
| `annotation.add` | `text`、`anchor?`（seq） | 仅展示的备注；绝不进入模型上下文。 |

载荷规则：

- 原生消息保留 SDK 类型及所有 provider 字段（签名、加密推理、内容顺序、工具 ID）。
  消息 ID 在会话内唯一。
- 补丁区分缺省、设置、清除；`false`、`0`、`""` 是值。拒绝未知字段。空补丁不写入。
- `outcome` 为 `success`、`failure` 或 `cancelled`；失败带有界错误，取消带原因。
- 从不存储：秘密、token 增量、进度刻度、心跳、重复快照，以及渲染器可推导的内容（预览、最后提示）。

`inference.start` 以 `input_through` 指向输入，而不是列出消息 ID，因此存储随对话长度线性增长。
系统提示和工具定义按内容去重存为 blob 并以摘要引用，使每次请求都可重建，无需单独的目录事件。

### 提交规则

1. **副作用跟随事实。** 携带工具调用的 assistant 消息在任何工具运行前提交；
   `inference.start` 在调用模型前提交；`permission.decide` 在被允许的工具运行前提交。
   提交失败则副作用不开始。
2. **结果与其结局一起提交。** 工具结果消息与其 `tool.finish`、生成的消息与
   `inference.finish` 各自同批。
3. **未完成可见，从不虚构。** 没有结果的工具调用、没有 finish 的 `inference.start`，
   恢复时报告为中断，不自动重试或补全。
4. **不明确的提交靠重读解决。** 出错后 writer 重读尾部；批次 ID 存在即已提交。

### 投影

纯投影器把已提交事件折叠为：

- **Context** — 发给模型的活动消息列表。
- **State** — 生效的覆盖与会话状态。
- **History** — 曾有过的所有消息及审计和备注，用于展示和检查。压缩前的消息保留在此。
- **Summary** — 缓存在 `.meta` 中的列表行数据。

恢复流程为 `Open → 投影 → 解析当前设置与信任 → 启动 runtime`。TUI、无界面和 ACP 共用；
ACP 在构建服务前用恢复的 `execution_cwd` 校验请求的 cwd。

## 关键流程

### 一轮对话

```text
commit [message.append user]
commit [inference.start]                       --> call model
commit [message.append assistant(tool calls), inference.finish]
  for each call: commit [permission.decide]    --> run tool
                 commit [message.append tool result, tool.finish]
```

### 子 agent

子 agent 是独立会话；父会话只记录它接纳的内容。

```text
parent: assistant tool call C (Agent)
child:  session.create { parent: {parent_sid, C} }   committed before the child runs
        ... its own independent events ...
parent: commit [message.append tool result for C, tool.finish { call_id: C, child_session }]
```

在工具调用之外送达的后续结果（后台完成、群组消息）是原父会话中的一条 `message.append`，
其 `source` 指向子会话的内容事件。投递与重试归传输层或群组收件箱；稳定的批次 ID 使重复投递幂等。
回放从不重建进程、回调或子会话所有权。

### 分叉

在源会话的 `seq N` 处分叉：

1. 以暂存名写入 `session.create { fork: {source, N} }`，随后原样复制事件 `2..N`，并复制被引用的 blob。
2. 清空 group hint；重命名事件文件到正式位置以发布。

位置和原生消息 ID 不变，因此无需改写任何引用。分叉相互独立：删除源会话不会破坏它，
复制前缀中未完成的尝试保持未完成。

### 压缩及其他上下文改写

除追加之外对活动上下文的任何改变——压缩、`/clear`、回退、清理旧工具输出——都是一条 `context.replace`。

```text
capture context at seq S --> generate summary (outside any lock)
commit [context.replace { reason: compaction, through: S,
                          entries: [summary, retained IDs...] }, inference.finish]
```

摘要生成期间在 `S` 之后提交的内容会接在 entries 之后留在上下文中，因此摘要不会过期，也无需拒绝。
entries 只能引用 `S` 时上下文中的消息，且结果上下文须保持每个工具调用与其结果成对。
失败或取消的压缩只写 `inference.finish`，上下文不变。保留的消息按引用而非复制，
因此压缩不会重复它们。

## 一致性与恢复

| 关注点 | 机制 |
| --- | --- |
| 原子分组 | 每组一个 sdk-go 批次：单行、带校验和、哈希链接到预期尾部。 |
| 持久性 | `Commit` 在文件 sync 后返回。仅限本地文件系统；不声称目录 sync 或断电认证。 |
| 单 writer | sdk-go 每会话 `flock`，由打开的文件持有，进程死亡即释放。第二个 writer 得到 `ErrBusy`。 |
| 崩溃恢复 | 下一个 writer 截掉未结束的末行。完整但损坏的行是错误，绝不跳过。比当前程序更新的记录类型阻止恢复。 |
| 资源 | blob 先写入并 sync，再写引用它的事件。 |
| Create / Fork | Create 是一个批次，崩溃后要么什么都没有，要么是有效会话。Fork 通过重命名发布。 |
| 缓存 | `.meta` 保存构建时的尾部（`count`、`hash`），仅当与事件文件一致时有效。缓存以原子替换写入。过期或缺失的缓存会重建，绝不信任。 |
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
- `Append` 是对话事件唯一的写入路径；压缩就是 `context.replace` 与 `inference.finish` 一起 `Append`。
  `session.create` 保留给 `Create` 和 `Fork`。
- `SetAttrs` 无需 writer 锁即可修改标题、归档和固定，因此选择器可以整理另一进程正打开的会话。
- 错误可区分：不存在、忙碌、已关闭、冲突、无效、不支持的版本、损坏、取消、存储错误。

当前 `Store`（`Create/Open/List/Fork/Delete`、`Append(transcript.Record)`、立即删除）演进为上述接口；
使用方保留窄的本地接口，通用部分移入 sdk-go。

## 生命周期与保留

用于整理会话的属性不是对话事实，因此存于 `<sid>.attrs.json` 而非事件：`title`、`archived_at`、
`pinned`、`last_resumed_at`。该文件在短暂的每会话锁下原子替换。最后活动时间取最后一个事件的
`time` 与 `last_resumed_at` 中较晚者；读取、扫描、缓存重建和恢复都不计入。

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

- **Delete** 需要 writer 锁（打开中的会话不能删除），且会话未固定。先写
  `trash/<sid>/manifest.json`（文件清单、deleted_at），再把事件文件、attrs 和 blobs 重命名进 `trash/<sid>/`。
  启动时，若 manifest 存在而文件未移完，则继续完成。子会话和分叉是独立会话，不受影响。
- **Restore** 反向移动。若该 ID 已有在线会话则失败；绝不覆盖或改名。
- **Purge** 删除超过期限的回收站目录，最后删 manifest。
- **Clean** 在 UI 可交互后运行保留和清除，每次运行有上限。`DryRun` 只报告将发生什么，不写入。
  自动保留默认只作用于当前 workspace，除非配置为项目级。
- **GC** 删除超过 24 小时的暂存文件和未被引用的 blob。无法确定归属的一律保留。

读取回收站中的会话需要 `visibility=trash`；`Open`、`Fork`、`Append` 在恢复前拒绝它们。

## 从 schema 4 迁移

新会话使用类型化事件。schema-4 transcript 通过适配器保持可读，并在首次恢复时、持有 writer 锁的情况下转换：

1. 按下表把记录映射到暂存事件文件，并复制 blob。
2. 验证投影出的上下文和历史与旧读取器一致。
3. 把原件移到 `legacy/<sid>.jsonl`，以相同会话 ID 发布新事件，使父链接和群组成员关系保持有效。

验证失败时，会话以旧格式保持只读，可作为其旧上下文的分叉继续。转换从不虚构事实：旧记录缺少的字段保持缺省。

| Schema 4 | 目标 |
| --- | --- |
| `session.started`、`session.forked` | `session.create` |
| SDK message / `message.appended` | `message.append` |
| SDK truncate + 重新保存的消息（`conversation.replaced`） | `context.replace`；与之前消息相同的重存消息成为 ID 项，其余成为新项。若有 `session.compacted` 指向摘要则 `reason: compaction`，否则为 `edit`。 |
| `session.state.patched` | `session.update`（model/mode/autopilot → config；tasks/group → state）；title → attrs |
| `inference.requested` / `responded` / `failed` | `inference.start` / `inference.finish` |
| `system.section.*`、`tool.added/removed` | 折叠进下一个 `inference.start` 的 instructions/tools blob |
| `tool.completed` | `tool.finish` |
| `permission.required` + `permission.decided` | `permission.decide`（`asked_at` 取自请求） |
| `hook.fired` | 结局为终态时映射为 `hook.finish`；仅异步派发的丢弃 |
| `skill.state.changed` | `session.update.state.active_skills` |
| `notice.added` | `annotation.add` |

## 备选方案

- **全局唯一事件 ID**（上一版草案）。需要分配、跨分区冲突检查，并在分叉时改写所有本地引用。
  位置身份 `(session, seq)` 加批次 ID 幂等，提供同样保证而没有这些成本。
- **工具、委派、权限请求的 start 事件。** 已提交的 assistant 消息在调用运行前就记录了它，
  子会话的 `session.create` 记录了委派，没有决定的调用即是待处理请求。单独的 start 事件是重复事实。
- **指令、工具、技能的目录事件。** 由 `inference.start` 引用的内容寻址 blob 使每次请求自描述，
  无需回放目录来解析摘要。
- **归档与固定作为事件。** 这会要求持有 writer 锁才能整理在别处打开的会话；它们也不是对话事实。
- **尾部移动时拒绝压缩。** 把捕获之后的消息并入结果，使过期不可能发生，而不是事后检测。
- **随机项目 ID + 注册表。** 注册表需要加锁更新与对账，仓库移动后仍需显式关联。派生键两者都不需要。
- **SQLite。** 2026-11-07 与 JSONL 对比测量（Apple M5，双方均持久 sync）：追加相同（≈4 ms），
  回放 1,000 条消息慢约 20%，列出 100 个会话在缓存热时快 3.5 倍、内存缓存丢失后慢 25 倍
  （JSONL 有持久化摘要），二进制增大 3.8 MB。不值得引入第二个存储引擎；只有在优化摘要缓存和
  回放分配之后、出现新证据时再考虑。
- **原地迁移或双写。** sdk-go 禁止在同一文件中混用旧记录与批次，双写会使故障模式翻倍。转换一次，保留原件。

## 验证

- 身份：worktree 共享项目；clone、submodule 和非 Git 子目录不共享；配置保持检出本地。
- 事件：每种事件类型往返无损；原生 provider 字段保留；回放无副作用；批次重试幂等、冲突失败。
- 在每次 write/sync/rename 处注入崩溃：部分批次、分叉中途、删除中途、恢复中途、转换中途，均恢复到已提交状态。
- 压缩期间并发追加；连续压缩；工具调用/结果成对完整。
- 删除源后分叉仍独立；子 agent 延迟结果。
- 生命周期：固定与忙碌保护、回收站期限、保留默认关闭、GC 保留未知文件。
- 旧格式：在真实 schema-4 会话上验证转换一致性，包括压缩和替换过的对话。

## 未决问题

- 载荷的精确 Go 类型，以及哪些部分（信封、投影器、批次辅助）属于 sdk-go。
- 仓库移动后的 `relink` 交互。
- 在测量大项目列表性能后，摘要缓存是否应改为每项目一个索引。
