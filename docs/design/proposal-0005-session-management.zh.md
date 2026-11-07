# PROP-0005: 会话管理与 SQLite 持久化

[English](proposal-0005-session-management.md)

## 状态与范围

草案 — 2026-11-07。文档先行；下述 API 和 SQLite 后端尚未实现。
当前存储仍是按 cwd 哈希分区的 sdk-go JSONL 日志。本提案改变持久化方式，不改变 agent 执行语义。

目标：保留现有场景，明确归属，并保持 API 和表结构精简。每项事实只有一个权威来源。
仅为独立生命周期或已证实的查询需求新增表，而非每个概念都建表。
派生摘要必须有明确的来源和重建规则。

## 模型与归属

```text
Project 1 ── N Workspace 1 ── N Session
                                  ├─ parent_session_id → 父 agent 会话
                                  ├─ fork_source_id    → 来源会话
                                  └─ 1 ── N Events（session_events，按 seq 排序）
                                            ├─ 会话：session.created / session.forked
                                            ├─ 消息：message
                                            │    └─ 追加有序原生消息列表
                                            ├─ 压缩：compaction
                                            │    └─ 完整原生摘要 + 源序号 + 有序保留消息 ID
                                            ├─ 配置/状态：config / state
                                            │    └─ 会话覆盖项 / 标题、待办、群组恢复提示
                                            ├─ 推理：inference.started / completed / failed
                                            └─ 工具/审计：tool.started / completed / failed、
                                                 notice.added、permission.requested / decided、
                                                 hook.completed、system.updated、tools.updated、skill.updated

Events ── 投影器 ──┬─ History：展示历史，包含压缩前消息
                  ├─ Snapshot：活跃上下文及持久状态，用于 resume
                  └─ Summary：可重建的会话列表摘要
```

每行事件只属于一个 Session，由 `type` 决定类型化的 `payload`；上面的分类不是独立的表。
消息是事件内容的一种，不代表全部事件。`message` 保存追加的原生对话内容，
`compaction` 保存其原生摘要；
`tool.completed` 补充执行/展示元数据，不重复保存工具结果正文。
父子关系和分叉关系连接的是 Session，不共享可变事件行。
History 和 Snapshot 是同一份事件的不同视图，不是各自保存一套数据。
这里的 Event 指持久化事实，并非每个流式 token 或 runtime 临时通知。
`Event` 是拟议存储封装；当前代码仍使用 `transcript.Record`，需要明确适配，不能直接改名。
名称能唯一表达语义即可：`message`、`compaction`、`config`、`state` 不加冗余动作后缀。
只有需要区分阶段时才保留后缀，如 `inference.started`；Session 归属由 `session_id` 表达。
这是目标命名，不是当前编码格式；导入时必须明确映射旧类型并保留语义。
归档属于状态；当前执行 cwd 属于会话状态，不改变归属工作区身份。

### 记录契约：事实完整，重复最少

事件只有在改变可恢复状态、构成用户可见历史，或解释重要执行/安全结果时才持久化，
不是每个 runtime 通知都落库。每种类型必须有生产方、消费方和最小载荷；
缺少其中任意一项，就没有充分理由保存。

| 事件族 | 最小持久化信息 |
| --- | --- |
| `session.created` / `session.forked` | 身份、初始位置/设置、父会话或固定的分叉来源 |
| `message` | 追加带稳定 ID 的有序原生消息列表；单条消息就是单元素列表 |
| `compaction` | `operation_id`、`source_seq`、含稳定 ID 和提供方内容的完整原生 `summary`、有序 `retained_message_ids`；一次完整成功转换 |
| `config` / `state` | 仅变化的类型化字段；区分未提供与显式清空 |
| `inference.started` / `inference.completed` / `inference.failed` | 尝试 ID、输入消息 ID 和非敏感请求设置；结果、用量和有界错误详情 |
| `tool.started` / `tool.completed` / `tool.failed` | 调用/尝试 ID、执行结果及必要的耗时/展示字段；参数和结果引用原生消息或资源 |
| `permission.requested` / `permission.decided`, `hook.completed` | 关联标识、限定作用域的决策/结果及有界原因；不复制提示词或工具载荷 |
| `system.updated`, `tools.updated`, `skill.updated`, `notice.added` | 变化的定义/状态，或有意义的通知及其消息位置；不在每次推理时重复未变化的定义 |

推理/工具执行前先持久化开始事件，并关联最终结果；重试使用不同尝试 ID。
取消必须明确记录为结果，不能算作成功。只有开始、没有结果表示中断/未知，
不能推断为确定失败或可安全自动重试。执行边界不同于持久化的助手工具调用消息：
调用消息可能在获准执行前就已存在。必要的开始事件无法提交时，不执行操作。
适配层必须建立这些目标保证，不能假设当前回调已经具备。

不保存逐 token 增量、加载/进度刷新、选择器导航、重复读取、空补丁、
未变化的快照或可重建的 UI 文案；最终原生消息仍须完整。
真实重复操作和内容相同的用户消息都要保留：只按事件/操作 ID 对重试去重，
不能仅按文本或时间戳去重。消息批次按原生消息身份去重，同一身份对应不同内容必须拒绝。
压缩通过已有消息 ID 保留内容，不重复复制正文，也不需要另建消息表。
排除密钥、凭据及无界诊断转储，保留有界诊断和必要资源引用。

### 归属规则

| 概念 | 负责内容 |
| --- | --- |
| Project | 稳定的逻辑身份、名称和描述 |
| Workspace | 本地目录/检出及其项目关联 |
| Session | 持久会话、归属工作区和写入所有权 |
| Event | 不可变、有序的对话与应用事实 |
| Projection | 对已提交记录前缀的可重建解释 |
| Runtime | 执行、取消和进程内资源 |

使用稳定的不透明 ID，而非路径、分支或远程 URL。新 Git 工作区在 worktree 根目录解析；
非 Git 根目录显式指定（默认是启动 cwd）。旧版精确 cwd 注册优先，且在显式协调前保持独立。
不得静默合并克隆、旧分区或信任决策。迁移位置时验证新路径及信任，不改写历史 cwd。

会话的归属工作区固定，但执行 cwd 可以跨项目改变，无需创建另一会话。
按执行 cwd 重新加载工具、设置、钩子和信任。恢复该位置需要带类型的 cwd 变更记录；
旧历史回退到初始 cwd。工作区缺失不妨碍查阅。

群组名单、收件箱和成员关系仍归群组子系统管理。会话记录只是恢复提示：
恢复时必须重新认领并协调；切换时释放活跃成员关系，分叉时清除继承的成员关系。持久化待办不是运行中的任务。
回放不会重启进程或重建回调；子会话历史会保留，但不保证重启后继续执行。
继续执行活跃子任务时验证父级/任务/会话所有权。延迟回调仍绑定原会话，而非 UI 当前选中的会话。

## API

以下是拟议的能力边界，不强制新增包。最终 Go 类型必须与 sdk-go 对齐；
使用方仅依赖其使用的方法。所有 I/O 都接受 context 取消。
下文仅为可读性而省略目录管理方法的 context 参数。

| 归属 | 操作 | 契约 |
| --- | --- | --- |
| Projects | `Resolve(location)`, `Get(id)`, `List(query)`, `Update(id, patch)`, `Archive(id, bool)` | 保守注册；管理元数据，而非配置 |
| Workspaces | `Resolve(location)`, `Get(id)`, `List(query)`, `Relocate(id, path)`, `Archive(id, bool)` | 验证位置和关联；不隐式合并 |
| Settings | `Resolve(ctx, target) (ResolvedConfig, error)`, `Update(ctx, target, patch) error` | 显式作用域、类型化补丁和来源追踪 |

```go
type SessionStore interface {
    Create(context.Context, CreateOptions) (*Session, error)
    Open(context.Context, string) (*Session, error)
    Get(context.Context, string) (Summary, error)
    List(context.Context, SessionQuery) (SessionPage, error)
    History(context.Context, string, HistoryQuery) (HistoryPage, error)
    Fork(context.Context, string, ForkOptions) (*Session, error)
    Archive(context.Context, string, bool) error
    Delete(context.Context, string) error
}

// Internal storage capability for replay, inspection and export.
type EventReader interface {
    Events(context.Context, string, EventQuery) (EventPage, error)
}

func (s *Session) ID() string
func (s *Session) Append(ctx context.Context, events ...Event) error
func (s *Session) Compact(ctx context.Context, change CompactionChange) error
func (s *Session) Snapshot() (Projection, error)
func (s *Session) Close() error
```

| 类型 / 操作 | 必需行为 |
| --- | --- |
| `CreateOptions` | 工作区 ID、初始 cwd、可选 ID/父会话及不含秘密的初始设置 |
| `Create` / `Open` | 返回持有写入所有权的句柄；Open 先认领，再加载一致的投影 |
| `Get` / `List` | 读取摘要而不认领写入所有权，包括由其他进程持有的会话 |
| `SessionQuery` | 项目/工作区/父会话/群组/sidechain/归档筛选、有界条数上限、不透明游标 |
| `Events` | 有序原始事件；游标绑定筛选条件和固定的 `ThroughSeq`，避免追加影响分页 |
| `History` | 该固定前缀的展示投影，包含压缩前历史；附件以引用提供 |
| `Snapshot` | 不可变的已提交上下文、应用状态和恢复位置；不含 UI 行或活跃资源 |
| `ForkOptions` | 可选目标工作区和源序号；否则捕获最新已提交前缀 |
| `CompactionChange` | `source_seq`（预期当前前缀）、稳定 `operation_id`、含稳定 ID/提供方内容的完整原生 `summary`、有序 `retained_message_ids` |
| `Append` / `Compact` | 共用一个原子写入实现；Compact 验证已完成的转换 |
| `Archive` / `Delete` | 归档追加状态；硬删除需要所有权，并拒绝存在活跃写入者的会话 |
| `Close` | 等待进行中的写入，且仅释放一次所有权 |

已打开的会话通过自己的 Append 归档。`Archive(..., false)` 恢复可见性。
项目/工作区存在子项时限制删除；归档不删除历史。父子关系必须无环。

展示条目分页前先投影历史：孤立的原始事件页无法重建压缩后的消息序列。绝不从 UI 历史恢复执行。
List 是按 `(updated_at, id)` 排序的实时键集列表，不是跨请求快照。

恢复流程协调 Open → 投影 → 当前配置/信任 → runtime。
TUI、无界面模式和 ACP 共用所有权规则。ACP 在构建服务前验证请求 cwd 是否与恢复的执行位置一致；
不一致时需要显式、获准的 cwd 变更。打开失败释放所有权；切换失败保持原选择。
区分不存在、忙碌、已关闭、冲突、无效、不支持的模式、数据损坏、取消和存储错误。

## 配置与存储分配

| 数据 | 权威来源 / 存储位置 |
| --- | --- |
| 项目描述和工作区注册 | `projects` / `workspaces` |
| 会话身份和查询摘要 | `sessions`；摘要派生自记录 |
| 原生消息、压缩、会话覆盖项、待办、通知和审计 | 类型化的 `session_events.payload` |
| 用户默认值及提供方/技能选择 | 现有用户文件，包括 `~/.san/settings.json` |
| 共享项目设置和指令 | 现有项目文件，包括 `.san/settings.json` |
| 本地/worktree/persona 覆盖项 | 现有作用域文件及解析器 |
| CLI/环境覆盖项 | 本次调用的内存；不得整体持久化 |
| 实际使用的请求设置 | 推理记录中白名单内的非秘密字段，不作为未来配置的权威来源 |
| 秘密和安全策略 | 现有认证/秘密/策略管理方，而非历史或检查点 |
| 大型工具输出和图像 | 记录引用的文件 |
| 可选恢复缓存 | `session_checkpoints` |

设置写入需选择作用域，仅更新该作用域的字段，绝不写入合并结果。
会话设置写入委托给持有所有权的 Session 句柄；文件写入保留跨进程协调。
补丁区分未提供、显式空值/false，以及源格式支持的移除。只读来源拒绝写入。
解析返回逐字段来源，合并值可包含多个来源。

保留当前用户 → 项目 → 本地文件解析、兼容来源、关联 worktree 的主检出本地覆盖项、
persona 和请求行为。合并按字段定义：映射逐键合并，权限规则取去重并集，
钩子遵循事件特定规则。会话选择不能覆盖安全检查。
当前详细顺序仍见[配置](../reference/configuration.md)。

恢复时解析当前策略和凭据；历史授权不是新的授权。模型/提供方缺失时，必须明确报错或选择替代项。
配置文件写入保持原子性；不承诺跨文件/SQLite 事务。请求记录描述实际使用的设置，
变更影响后续解析。不得创建配置文件的可编辑数据库镜像。
将其权威来源迁入 SQLite 需要单独的迁移决策。

## 数据表

以下是逻辑模式，不是可执行迁移。ID 为 TEXT；时间为 UTC Unix 毫秒。
列默认 NOT NULL，除非标记 `?`。每个连接都启用外键。
JSON 载荷带类型和版本，不是任意元数据容器。

### `projects`

| 列 | 类型 / 约束 | 含义 |
| --- | --- | --- |
| `id` | TEXT PK | 稳定项目身份 |
| `name` | TEXT | 显示名称 |
| `description` | TEXT DEFAULT '' | 描述，不是隐式模型指令 |
| `created_at`, `updated_at` | INTEGER | 注册 / 元数据修改时间 |
| `archived_at` | INTEGER ? | 可见性标记 |

不设当前 cwd、模型、分支或活跃会话列。

### `workspaces`

| 列 | 类型 / 约束 | 含义 |
| --- | --- | --- |
| `id` | TEXT PK | 稳定本地身份 |
| `project_id` | TEXT FK projects, DELETE RESTRICT | 逻辑归属 |
| `root_path` | TEXT UNIQUE | 规范化的本地根目录 |
| `kind` | TEXT CHECK directory/git_worktree | 位置类型 |
| `created_at`, `last_opened_at` | INTEGER | 注册 / 最近打开时间 |
| `archived_at` | INTEGER ? | 可见性，不表示文件系统删除 |

索引 `(project_id, id)`。路径唯一性包含已归档行；复用路径需要显式协调。
解析和信任共用规范化及符号链接规则；不得以路径转小写来近似文件系统的大小写行为。

### `sessions`

| 列 | 类型 / 约束 | 含义 |
| --- | --- | --- |
| `id` | TEXT PK | 会话身份 |
| `workspace_id` | TEXT FK workspaces, DELETE RESTRICT | 不可变的归属工作区 |
| `parent_session_id` | TEXT ? FK sessions, DELETE SET NULL | agent 父子关系 |
| `fork_source_id` | TEXT ? FK sessions, DELETE SET NULL | 分叉来源 |
| `fork_source_seq` | INTEGER ? CHECK >= 0 | 捕获的源前缀 |
| `initial_cwd` | TEXT | 历史启动位置 |
| `created_at`, `updated_at` | INTEGER | 创建 / 已提交活动时间 |
| `archived_at` | INTEGER ? | 派生归档状态 |
| `title` | TEXT DEFAULT '' | 派生显示标题 |
| `last_seq` | INTEGER DEFAULT 0 CHECK >= 0 | 最新已提交序号 |

索引：`(workspace_id, updated_at, id)`、`(parent_session_id)`、`(fork_source_id)`。
项目查询通过关联 workspaces 完成，不重复存储 `project_id`。
删除来源导致引用列清空后，创建/分叉记录仍保留历史身份；不级联删除后代。
`initial_cwd` 可以是子目录，因此与工作区根目录不冗余。

实现前依据现有查询确定模型/预览/群组/sidechain 摘要列。
每个派生列都有投影器，不设独立 setter；不得为减少列数而丢弃现有筛选行为。

### `session_events`

| 列 | 类型 / 约束 | 含义 |
| --- | --- | --- |
| `session_id` | TEXT FK sessions, DELETE CASCADE | 所属会话 |
| `seq` | INTEGER CHECK > 0 | 会话内提交顺序 |
| `id` | TEXT | 稳定事件身份 |
| `type` | TEXT | 事件类型标识 |
| `schema_version` | INTEGER CHECK > 0 | 载荷编解码版本 |
| `created_at` | INTEGER | 事件时间，不是排序键 |
| `payload` | TEXT, validated JSON | 完整的事件专属内容 |

主键 `(session_id, seq)`；唯一约束 `(session_id, id)`。无编解码要求时，不在载荷中重复封装字段。
原生消息 ID 不同于记录 ID。编解码器必须保留签名、加密推理、图像/内容顺序、工具 ID
及受支持的提供方字段。当前 `Record.Message`/`Messages` 使用 `json:"-"`；
直接序列化 Record 会丢失消息。通过经验证的无损导入映射保留旧替换语义，
而非在目标模型中增加替换事件；引入消息父子树是单独的决策。

### `session_checkpoints` — 后续选项，不属于第一阶段

仅保留未来表结构参考，初期不建表，resume 不依赖它。
第一阶段从事件恢复，压缩能力不需要检查点也必须完整支持。

| 列 | 类型 / 约束 | 含义 |
| --- | --- | --- |
| `session_id` | TEXT PK FK sessions, DELETE CASCADE | 唯一的最新检查点 |
| `through_seq` | INTEGER CHECK >= 0 | 完整投影前缀 |
| `projector_version` | INTEGER CHECK > 0 | 投影和快照格式 |
| `payload` | BLOB | 完整持久化投影，不含秘密/活跃资源 |
| `created_at` | INTEGER | 快照时间 |

仅在测量恢复成本后添加。在一致快照中验证 `through_seq <= last_seq`；
较旧的重建不能替换较新的检查点。须包含持久化待办和恢复位置，而非仅有消息。
无效/不兼容的检查点回退到记录。除主键外不需要额外索引。

初期不另设并行的 `session_state`、`project_state`、`messages`、`parts`、`runs`
或通用设置表。SQL 迁移、记录编解码和投影器版本是独立契约；数据库模式迁移需要版本台账。

## 压缩：第一阶段的会话能力

压缩只改变有效消息序列，不改写或删除已有历史、配置、状态及附件。
模型摘要本身可能遗漏细节；这里保证的是持久化原文无损，不承诺摘要或模型理解无损。
`Session.Compact` 使用共享追加路径；摘要生成在 SQL 事务之外完成。

一次成功压缩只提交一个 `compaction` 事件，不再拆成替换和完成事件：

| payload 字段 | 含义 |
| --- | --- |
| `operation_id` | 稳定操作 ID，用于幂等重试 |
| `source_seq` | 生成摘要时捕获的已提交会话版本 |
| `summary` | 完整原生摘要消息，包括稳定消息 ID、角色、内容及提供方字段 |
| `retained_message_ids` | 按原有效序列顺序保留的消息 ID，不复制正文 |

投影结果为「摘要 + 引用的保留消息」。引用必须指向同一会话源版本中已有的不可变消息，
包括仍有效的先前压缩摘要；新摘要进入消息索引和 History，无需另写一条消息事件。
原文、工具结果、图像、签名、内容顺序和消息 ID 继续保留，工具结果不能成为悬空引用。

提交前校验源版本未变、摘要身份无冲突、引用存在且唯一、顺序合法，
并验证保留的工具调用/结果关系符合原生消息约束。缺失、跨会话或越过源版本的引用必须拒绝。
事件和派生摘要/序号在同一事务提交，提交后才发布内存投影。
同一操作及内容的重试不新增事件；同一操作 ID 对应不同内容报冲突。
先检查操作是否已提交，再检查源版本，避免把成功操作的重试误判为过期。
拒绝已退役会话/runtime 的延迟结果。

失败、取消或无需压缩时不提交压缩事件，有效上下文不变；对应执行记录仍可解释失败。
提交后崩溃，resume 必须恢复已提交结果，即使 UI 尚未显示。
提交结果不明确时，先确认结果，再重试或接受新写入。
自动、手动、runtime 未启动及连续压缩都使用同一规则。
分叉复制完整选定前缀及所需引用，ID 在子会话内解析；后续任一方压缩不影响另一方，压缩不清理旧附件。
检查点仅用于加速，不能替代原文或压缩事件；无需专门的压缩表。

旧版摘要追加/边界/截断必须通过显式转换保留恢复结果、完整历史、原生 ID 和审计来源。
非压缩的旧对话替换不能伪装成压缩；新会话导入或子会话快照仅在证明等价时转为消息追加。
无法无损表达的记录必须拒绝导入并保留原文件，待兼容映射确定后才能切换，不能静默丢弃。
实现验收须比较旧/新投影器的有效消息、历史和引用，并覆盖连续压缩、失败/重试、
缺失引用、恢复和分叉；目前仅定义契约，不声称这些保证已由实现验证。

## 一致性与生命周期

- 建议每个 San 数据主目录使用一个本地数据库。第一阶段确定驱动/路径；初期保证不涵盖网络文件系统。
  使用 WAL、有界忙等待和显式持久性策略。SQLite 仍只允许一个并发写入者。
- Append 原子地插入记录并更新摘要/序号，然后发布已提交的内存快照。
  相同记录 ID/内容具有幂等性；同一 ID 对应不同内容时返回冲突。
  提交结果不明确时，必须先确认结果再进行新写入。取消不证明回滚。
  影响状态的未知记录版本阻止恢复。
- 每会话的跨进程所有权独立于 SQL 事务；使用 SDK 支持且不绑定 JSONL 路径的机制。
  Close/进程死亡释放所有权。绝不跨推理、工具或审批持有事务，
  也不使用无隔离令牌、仅基于时间戳的租约。外部工具执行不保证恰好一次。
- 引用资源前先原子发布资源。分叉保护固定源前缀免遭删除，在发布子会话前复制资源并改写引用。
  清除继承的群组成员关系。清理排除进行中的操作，并可重试。删除先提交，再回收资源。
- 将一致的 SQLite 快照与引用资源一同备份。WAL 写入期间只复制主数据库文件并不足够。

## 归属边界与落地步骤

sdk-go 负责通用有序存储、原生编解码器、所有权和事务原语。
San 负责身份解析、配置/信任、应用记录和投影。定义 SDK/San 摘要的原子更新，
不让 SDK 导入 San，也不让 UI/runtime 发出 SQL。
以查询/导出替代 `TranscriptPath` 和文件存在性假设；JSONL 保留为交换格式，而非第二个可写权威来源。

1. **确定契约：** 审查 sdk-go 和现有调用方；确定身份规则、查询驱动的列、编解码器、所有权、
   数据库范围和原子投影。此时即纳入 Compact、历史/恢复/分叉及失败语义。
2. **引入 API 接缝：** 保留文件后端行为，同时消除路径假设；统一投影和压缩入口。
   明确哪些更强的原子性要求新后端支持。
3. **实现 SQLite：** 实体、记录、压缩和摘要；开发期间对两种后端运行共享行为用例。
4. **导入并切换：** 独占来源，保留 ID/资源和旧文件，记录导入进度/来源，验证重试而非覆盖。
   逐个报告损坏/不支持的会话。切换前验证旧格式无损映射与旧投影器等价性，
   无法映射的来源保留且不宣称完全兼容。绝不静默合并项目。
5. **优化：** 添加检查点/索引前先测量；验证切换后停用旧写入者，并更新包文档。

迁移后的会话只允许一个后端写入。SQLite 产生新写入前，可用原件回滚；之后回滚前需先导出较新历史。
迁移记账模式依据编解码器/旧 ID 审查确定。根据原始记录分类旧父级字段，
不要猜测其表示父子关系还是分叉。

## 验收与待定事项

| 场景 | 必需结果 |
| --- | --- |
| 根目录、嵌套 cwd、克隆/worktree 和旧分区 | 身份可预测；无静默合并或信任继承 |
| 移动、目录缺失、路径复用、跨项目 `cd` | 历史可查阅；显式协调；刷新执行策略 |
| TUI/无界面模式/ACP 和竞争进程 | cwd 经验证，共用单写入者所有权 |
| 群组切换/重新认领被拒；后台延迟完成 | 不恢复失效成员关系，无跨会话回调 |
| 子会话历史和分叉；父会话删除 | 独立身份/资源；历史不意味着存在活跃任务 |
| 重复/失败压缩、重试和无效引用 | 单个原子事件；与旧投影器的上下文/历史等价；状态/配置/附件不变；拒绝无法解析的 ID |
| 配置变更和权限撤销 | 正确的作用域写入，恢复时采用当前安全策略 |
| 展示历史、图像和并行工具 | 完整浏览，不扩大恢复后的模型上下文 |
| 崩溃、重复写入、清理和错误检查点 | 一致的记录/摘要；可恢复缓存；显式失败 |
| 调用中断、重试和内容相同的重复输入 | 开始/结果可关联；未知执行保持未知；去重不丢失独立动作 |
| 流式/进度通知和未变状态 | 最终消息完整，无逐 token 事件、空补丁或重复快照 |
| 导入、重复导入、导出和回滚 | 保留 ID/内容，只有一个可写权威来源 |

以现有 session/transcript/app 测试为基线。优化前检查选择器响应速度和回放成本。
文档不声称 SQLite 测试已通过。

实现前需确定位置迁移/worktree 关联的交互、最终摘要字段、SDK 事务集成及迁移记账。
若事务/查询收益不足以证明迁移合理，JSONL 仍是有效替代方案。
基于文件的 SQLite 索引仍需处理跨存储一致性；按项目分库可隔离故障，但会增加关联和全局查询的复杂度。
避免推测性模式设计。
