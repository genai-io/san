# PROP-0005: 会话管理与文件持久化

[English](proposal-0005-session-management.md)

## 状态与范围

草案 — 2026-11-07。目标是以文件/JSONL 实现存储中立的会话/事件 API。
SQLite 实验实现及驱动依赖从本次交付中移除，只保留历史测量作为选型依据。
本次保留通用文件存储能力，不引入数据库迁移或后端选择 UI。

已实现第一阶段：`OpenStore` 项目注册、Git worktree 共享归属与项目设置、非 Git 精确路径归属，
以及 schema 4 中可选的项目/workspace 身份字段与摘要缓存升级。旧 cwd 分区不自动迁移。
当前仍使用 SDK 原生记录及 transcript/truncate 语义；18 类新事件、完整目标 API、
原子批次、回收站和自动保留策略尚未实现。Runtime hooks 和 inspector 保持现有 JSONL 集成。
新格式切换前须先补齐 SDK 原子批次和可阻断执行的持久化接纳边界，不能仅重命名旧事件。

每项事实只有一个权威来源。持久化对话变化、必要执行/权限审计及不可重建说明，
而非通用 runtime 日志。摘要和索引必须可重建。

## 身份、目录与配置

```text
Project 1 ── N Session ── N Events（按 seq 排序）
                 ├─ workspace：检出/归属路径属性
                 ├─ parent_session_id：agent 父子关系
                 └─ fork_source_id + fork_source_seq：捕获的来源前缀

Events → 投影器 → History （完整展示历史，包括压缩前内容）
                → Snapshot（有效上下文及持久恢复状态）
                → Summary （会话列表元数据）
```

Workspace 不是实体、服务或物理目录层。Session 是可恢复对话，不是一次进程或
agent 运行；Close 释放句柄，不终结会话。初期没有 `session.end` 事件。
Runtime 负责执行和取消；持久待办、权限决定及群组提示不是活跃资源。

项目使用注册时分配的稳定不透明 ID，与路径查询键分开：

| 启动位置 | 内部注册规则 |
| --- | --- |
| Git | 规范化的绝对 Git common directory；先按 Git 定义的基准解析相对返回值。主检出、linked worktree 及其子目录共享 Project；独立 clone 和 submodule 保持独立。 |
| 非 Git | 精确的规范化启动 cwd 就是查询键、项目根及 workspace。同一路径复用注册；子目录启动属于独立 Project。不查找最近祖先。 |
| 两者 | 绝对路径和符号链接规范化与信任检查一致；不转小写。运行时 cwd 变化不重新分配归属。 |

`project_id` 和 `workspace` 是固定创建属性。Git workspace 是检出根，
`initial_cwd` 可在其下。可变 `execution_cwd` 可以跨项目，但不迁移会话。
从状态恢复执行位置，旧历史回退到初始 cwd；按执行位置解析工具、配置、Hook 和信任。

移动、路径复用及 Git 元数据缺失/变化须显式验证并协调/选择注册。
不能静默转移身份、合并旧精确 cwd 分区或改写历史路径。
关联不授予信任；检出缺失不影响历史查阅。

既有配置模块解析并保留 `config_root`：

- Git 使用**原始主检出**，即使首次从 linked worktree 启动，也根据仓库/worktree
  元数据解析。Git common directory 标识项目，不是共享设置目录。
- 非 Git 使用上述精确启动根。共享来源缺失/有歧义时须报错或显式选择/协调，
  不能回退到任意 worktree。
- 共享设置/指令（包括 `.san/settings.json`）来自 config_root，再通过既有解析器
  应用允许的目标 worktree 本地覆盖。分支内副本不是另一共享权威；
  注册只保存来源位置，不另存可编辑的设置副本。

当前已接入 settings.json 的共享来源及目标局部写入；MCP、插件、技能和指令文件仍由
各自既有加载器处理，统一来源适配尚未完成，不声称所有项目文件已经共享。注册锁等待
目前不支持中途取消，注册原子发布不承诺断电持久性；路径完全不变的仓库替换无法自动识别。

保留当前用户 → 项目 → 本地解析、兼容来源、linked-worktree 主检出本地覆盖、
persona 及逐字段合并规则，详见[配置](../reference/configuration.md)。
写入选择一个作用域，仅更新其字段，跨进程协调并拒绝只读来源；不能写回合并结果。
解析保留逐字段来源信息。

会话覆盖影响后续解析，不修改进行中的请求。恢复使用当前策略和凭据，不能复用历史授权。
缺失模型/提供方须明确报错或选择替代项。配置文件与事件之间不承诺事务；
历史不复制秘密、安全权威或整棵设置树。

旧布局按精确 cwd 的哈希分区，日志为 `transcripts/<sid>.jsonl`，摘要为
`transcripts/<sid>.jsonl.meta`，附件按类型及会话隔离。新注册已使用稳定 ID 和项目文件，
保留原日志/附件路径约定；回收站仍是目标布局的一部分：

```text
~/.san/
└── projects/
    └── <project-id>/
        ├── project.json                 # 身份、查询键、config_root；不是设置副本
        ├── transcripts/
        │   ├── <sid>.jsonl               # 权威事件记录
        │   └── <sid>.jsonl.meta          # 可重建摘要
        ├── blobs/
        │   ├── image/<sid>/              # 会话保留的图像
        │   └── tool-result/<sid>/        # 大型工具输出
        └── trash/<sid>/                  # 删除代次、恢复清单及完整保留数据
```

`project.json` 已用于新注册；`trash/` 仍属拟议。项目注册文件可扫描重建查询索引；
索引不是另一身份来源。旧 `<cwd-hash>` 分区不自动改名或搬迁。
同一项目的 worktree 共用该目录，以 Session.workspace 区分；父子会话日志同级，
靠 parent_session_id 关联。回收站保留日志、附件和必要元数据；转换须可恢复，不能假设多文件移动原子。
共享配置仍位于 config_root 的项目文件中，由现有配置模块管理，不复制到此目录。

内部目录负责项目名称/描述、注册及分区位置，不负责配置或当前 cwd。
已有分区保持独立、可读，直到获准协调；不隐含自动重命名或导入。
存在会话、回收站或未完成转换时阻止项目移除。删除会话不删除检出目录；
父子关系不意味着级联删除。

## 事件契约

Event 是不可变的持久事实，不是临时通知，也不是给 `transcript.Record` 改名。
命名采用 `<object>.<action>`：apply 记录已接受转换；finish 记录终止结果，不一定成功。

| 封装字段 | 类型及含义 |
| --- | --- |
| `id` | 全局唯一字符串，提交请求前分配；持久化重试保持不变 |
| `session_id` | 归属会话 ID；项目/workspace 通过会话获取 |
| `seq` | 连续的正整数会话内提交顺序，提交时分配 |
| `type`, `schema_version` | 事件类型及正整数载荷编解码版本 |
| `created_at` | UTC Unix 毫秒；不是排序键 |
| `source_id?` | 本地或外部直接来源**事件 ID**，不是会话、请求、原生消息 ID 或旧序号 |
| `outcome?` | 仅 inference/tool/delegation/hook finish 必填：success/failure/cancelled |
| `payload` | 经验证的类型化 JSON，不重复封装字段 |

Start 事件 ID 就是操作身份。Finish 通过 `source_id` 指向 start，
permission.decide 指向 request。生成消息/摘要指向生产方 start；
接收消息指向源内容，委派输入则指向 delegation.start。直接用户输入可无来源。
原生 call_id/turn_id 保留在载荷中。来源链接表示溯源，不表示所有权或完整依赖图。

以下唯一类型表中，`?` 表示可选，其余载荷字段必填。
原生消息、工具定义及嵌套状态保留 SDK/产品类型，不简化成通用字符串。
具体 Go 载荷类型仍须通过 SDK 对齐检查。

| 事件 | 类型化载荷字段 | 应用效果 / 边界 |
| --- | --- | --- |
| `session.create` | project_id, workspace, initial_cwd, agent_id, sidechain；parent_session_id?, fork_source_id?, fork_source_seq?, initial_config? | 初始化身份/投影及来源；不启动 agent 或重复复制。 |
| `message.append` | messages：非空有序原生消息列表；source_message_ids?：逐项对齐的源原生 ID | 索引内容，追加历史/有效序列，派生预览；不执行内嵌工具调用。 |
| `compaction.apply` | source_seq, summary：原生消息, retained_message_ids：有序列表 | 索引摘要；有效序列变为摘要 + 保留消息；保留历史/config/state/资源。 |
| `config.update` | patch: ConfigPatch | 更新下次解析使用的会话覆盖，不修改 User/Project 文件或进行中请求。 |
| `state.update` | patch: StatePatch | 更新指定状态/摘要；cwd 变化要求刷新环境，不改变归属或权限。 |
| `inference.start` | turn_id, input_message_ids, request_config, instruction_digest, toolset_digest | 登记未完成尝试及实际非秘密请求来源；不请求模型。 |
| `inference.finish` | stop_reason?, usage?, duration_ms?, error?, reason? | 结束尝试并更新审计/用量；输出留在消息事件，不自动重试。 |
| `tool.start` | call_id, input_ref | 登记执行；input_ref 标识持久调用/参数；不授权或执行。 |
| `tool.finish` | call_id；duration_ms?, error?, reason?, details? | 结束尝试并关联类型化执行/展示详情；结果正文留在消息/资源。 |
| `delegation.start` | child_session_id, input_ref | 登记一次已接纳的子会话执行/继续执行；不启动子会话或复制其流。 |
| `delegation.finish` | result_event_id?, error?, reason? | 结束尝试并关联本地已接纳结果，不终结子会话或重复结果。 |
| `permission.request` | operation_ref, scope | 记录关联受保护操作的待决审计；回放不弹请求。 |
| `permission.decide` | decision, scope；reason? | 以 allow/deny/cancelled 完成审计；不恢复历史授权。 |
| `hook.finish` | hook_name, trigger；duration_ms?, reason?, effect_refs? | 记录已知结果及单独归属的效果引用；不重跑 Hook/效果。 |
| `instruction.update` | upsert：具名指令片段, remove：片段名称；source? | 更新请求摘要所引用的历史目录，不修改当前文件或消息。 |
| `toolset.update` | upsert：原生工具定义, remove：工具名称；source? | 更新历史工具目录；不安装或启用工具。 |
| `skill.update` | changes：具名、类型化技能启用状态；source? | 记录观察到的启用状态供查阅，不加载技能或编辑偏好。 |
| `annotation.add` | text；anchor?：消息 ID 或事件序号 | 锚点或会话级持久展示说明；不进入模型上下文、修改状态或执行。 |

ConfigPatch 允许 provider、model、generation_options、mode 和 autopilot_policy。
生成参数/策略使用版本化白名单产品字段，不接受任意提供方 JSON。
StatePatch 允许 title、archived_at、pinned、todos、autopilot_progress、
execution_cwd 和 group_hint。提示只含重新认领用群组/成员身份，不是名单或权限权威。
最近提示词预览由消息派生。

补丁区分未提供、设置和清空；false/零/空是显式值。拒绝未知或跨归属字段。
具名目录变更要求名称唯一、upsert/remove 不相交，请求摘要可解析；空目录是已知值。
未变化的补丁/定义不产生事件。

校验及记录规则：

- 创建唯一且位于 seq 1；父子关系无环。分叉源 ID/序号同时缺省或指向非空源前缀。
  初始配置不含秘密。
- 原生消息 ID 非空且本地唯一；内容冲突须失败。保留签名、加密推理、图像/内容顺序、
  工具 ID 及支持的提供方字段。当前 Record.Message/Messages 使用 `json:"-"`，
  直接序列化 Record 会丢失内容。
- 每个消息批次只有一个来源，混合来源须拆批。来源不可变，不从相邻位置/时间推测。
  本地引用解析到已提交前缀或同批更早的已验证条目；input_ref 指向不可变原生调用
  或已持久化调用内容。必需内容缺失则失败。
- Finish 须匹配同会话 start/类别，工具 call ID 也须匹配；每个 start/request 至多
  一个终止结果。失败需有界结构化错误，取消需原因；权限作用域不能扩大请求。
  未知结果保持未知，派发确认不是 finish。
- 可信适配层在接纳时验证外部来源；回放只读本地内容，不依赖外部源继续存在。
  导入时来源不可用须显式兼容处理，不能伪造引用。
- 派发前提交 start；持久化失败则不执行。输出及 finish 同时已知时合批提交。
  较早完整输出可在崩溃后保留但无已知 finish；回放不重试、不产生副作用。
- 同事件 ID/内容幂等，冲突复用失败。新执行及有意重复输入使用新 ID，即使文本相同。
  不保存 token 增量、心跳、进度刷新、空补丁、重复快照或可派生 UI 文案；
  排除秘密及无界诊断。渲染器消费事实，不能作为持久化生产方。

回放只投影已提交事实，不调用模型/工具/Hook、不弹授权、不重投或追加事件。
提交后由实时协调层处理运行环境变化；恢复只解析一次最终配置/信任。

## 当前记录 → 目标事件

当前存储结合 SDK message/truncate/应用记录和有 **19 个类型常量**的 San 模式
`"4"`，不是这 18 类事件。见 [records.go](../../internal/transcript/records.go)
及 [read.go](../../internal/transcript/read.go)。当前读取 ID 是日志位置。
下表是语义适配提案，不是改名迁移。

| 当前记录 | 目标 / 兼容要求 |
| --- | --- |
| `session.started`, `session.forked` | 一个 session.create，验证父级/分叉来源 |
| `message.appended`（SDK message） | message.append，保留原生字段/ID 及已知来源 |
| `session.compacted`, `conversation.replaced`（SDK truncate） | 联合验证摘要/边界/截断后映射 compaction.apply；通用替换不一定是压缩 |
| `session.state.patched` | 后续行为拆为 config.update，事实拆为 state.update；预览派生 |
| `inference.requested` | inference.start，验证请求关联/来源 |
| `inference.responded`, `inference.failed` | inference.finish，保留已知结果及记录的尝试关系；不推测缺失 start |
| `system.section.added`, `system.section.removed` | instruction.update upsert/remove |
| `tool.added`, `tool.removed` | toolset.update，不是执行 |
| `hook.fired` | 仅已知终止结果映射 hook.finish；async 派发不等于完成 |
| `permission.required`, `permission.decided` | permission.request/decide；转换 permit/reject，验证关联；即时决定可能没有请求 |
| `skill.state.changed`, `notice.added` | skill.update / annotation.add，保留观察状态及有意义的锚点语义 |
| `tool.completed` | 仅已知结果/start 关联时成为 tool.finish 详情；当前载荷本身不足以证明 |
| 无专用模式 4 常量 | tool.start 和 delegation.start/finish 需要新生产方 |

缺失 start、全局 ID、关联或结果须显式兼容映射，不丢弃未匹配记录或伪造事实。
无法无损映射则阻止导入和切换，保留原件。须证明新旧有效上下文、完整历史、
原生 ID、资源及 inspector 来源等价，包括旧替换语义。

## API

公开模型是具体的项目作用域 `*Store` 和持有所有权的 `*Session`，
不公开 Projects、Settings、Workspaces、SessionStore、EventReader 或 History 服务。
使用方可定义窄接口；最终原语/类型须与 sdk-go 对齐。

当前 internal/session.Store 已有 Create、Open、List/ListContext、Fork、Delete、Close；
Open/Fork 还返回 History。Session.Append 接收单个 transcript.Record，Session.Close 已存在；
transcript.Store.Records 提供原始记录。当前 Delete 立即删除历史。
以下签名及可恢复 Delete 均为拟议契约。

```go
func OpenStore(ctx context.Context, dir string) (*Store, error)

func (s *Store) Create(ctx context.Context, opts CreateOptions) (*Session, error)
func (s *Store) Open(ctx context.Context, id string) (*Session, error)
func (s *Store) Get(ctx context.Context, id string) (Summary, error)
func (s *Store) List(ctx context.Context, query SessionQuery) (SessionPage, error)
func (s *Store) Events(ctx context.Context, id string, query EventQuery) (EventPage, error)
func (s *Store) Fork(ctx context.Context, id string, opts ForkOptions) (*Session, error)
func (s *Store) Archive(ctx context.Context, id string, archived bool) error
func (s *Store) Delete(ctx context.Context, id string) error
func (s *Store) Restore(ctx context.Context, id string) error
func (s *Store) Clean(ctx context.Context, opts CleanOptions) (CleanResult, error)
func (s *Store) Close() error

type CleanOptions struct {
    DryRun bool
    IDs    []string
    Limit  int
}

func (s *Session) ID() string
func (s *Session) Append(ctx context.Context, events ...Event) error
func (s *Session) Compact(ctx context.Context, change CompactionChange) error
func (s *Session) Snapshot() (Projection, error)
func (s *Session) Close() error
```

OpenStore 规范化启动 dir，内部解析/注册项目并固定作用域。
除显式选定的 Fork 源外，所有 ID 须属于该项目。
I/O 支持 context 取消；Snapshot 读取不可变的内存已提交投影。

| 类型 / 操作 | 选项及行为 |
| --- | --- |
| CreateOptions | 可选 ID/父会话、非秘密初始设置、workspace/initial cwd。默认取捕获的检出/根及启动 dir；另一 workspace 须解析到同项目，cwd 默认取该位置。Store 提供 project_id；验证信任及委派 source_id。 |
| Create / Open | 先取得 writer 所有权，再加载一致状态。Open 保留已有 workspace/cwd，不替换为构造器默认值。 |
| Get / List / SessionQuery | 无需认领 writer 即读本项目各 workspace 摘要。支持 workspace/父会话/群组/sidechain/归档、活动（recent/all）、可见性（live/trash/all）、有界上限及不透明游标。 |
| Events / EventQuery / EventPage | 按 seq 排序的有界原始事件；游标绑定项目/会话、筛选、生命周期代次及经验证固定 ThroughSeq/前缀身份。后续追加不改变前缀；内容变化/不可用明确失败。 |
| Snapshot | 有效上下文、应用状态及恢复位置，不含展示行或活跃资源。 |
| ForkOptions | 源序号默认最新已提交前缀；同项目 workspace 默认源位置。接收调用者是目标；跨项目须显式提供另行打开的源 *Store，目标默认值取接收方。验证双方关联/信任。 |
| CompactionChange | 预期 source_seq、稳定事件 id、含稳定 ID 的完整原生 summary、有序 retained_message_ids、可选 source_id 指向生成推理 start。 |
| Append / Compact | 共用经验证原子批次路径；Append 支持 apply + inference.finish，Compact 封装单次转换。创建仅由 Create/Fork 负责。 |
| Delete / Restore / Clean | 可恢复删除 / 验证后恢复原 ID / 有界策略清理。Clean IDs 只缩小范围；Limit 有有限默认值及上限；DryRun 只读。 |
| Session.Close / Store.Close | 等待写入结束，Session 只释放一次所有权。先关闭 Session；Store 等待操作结束，有 writer 句柄时拒绝关闭。 |

List 是按 `(updated_at, id)` 排序的实时键集列表，不是跨请求快照；
未知活动放入按 ID 排序的稳定分组。History 从完整固定前缀投影，之后才对展示分页，
不能用孤立原始页或筛选流重建。不能从 UI 历史恢复执行。
未来支持完整历史的检查点可加速；仅有效上下文的缓存不能替代 History。

| API / 生产方 | 持久映射 |
| --- | --- |
| Create / Fork | 一个 session.create；Fork 导入/重映射继承事实 |
| Compact / 带审计 Append | compaction.apply；接受审计结果时合批 apply + inference.finish |
| Archive | 独占下写 state.update.archived_at；已打开会话使用自身 Append |
| 标题/固定/待办/cwd/配置管理方 | 经 Append 写类型化 state.update/config.update；文件配置仍归既有管理方 |
| 模型/工具/委派适配层 | Start、已接纳消息和已知 finish |
| 审批/Hook/请求准备/技能/应用 | 对应类型化审计/目录/注释事件，不增加独立 Store 动词 |
| Get/List/Events/Snapshot、Open/Close | 无对话事件；成功显式 resume 活动另存控制元数据 |
| Delete/Restore/Clean | 外部生命周期元数据，不伪造对话事件 |

恢复流程为 Open → 投影 → 当前配置/信任 → runtime。
TUI/无界面/ACP 共用所有权规则；ACP 构建服务前校验请求 cwd 与恢复执行位置。
不一致须显式获准变更。打开失败释放所有权；切换失败保留原选择。
区分不存在、忙碌、关闭、冲突、无效、不支持模式、损坏、取消和存储错误。

## 子 agent、分叉与压缩

**子 agent。** 每个子 agent 拥有独立流。delegation.start 前预留子 ID；
派发前创建/认领并验证父级/任务/子会话所有权。每次继续执行使用新 start ID。
父会话只保存协调及已接纳内容，不转存子事件流。delegation.finish 的 source_id
指向 start，result_event_id 指向本地已接纳结果；已知结果 + finish 合批，不虚构空输出。

跨会话接纳独立保存内容/资源，使用新接收方本地原生 ID 及对齐的 source_message_ids。
保留提供方字段和工具身份。可信投递层跨重试/重启保留稳定接纳事件/消息 ID；
内容/来源/接收方变化即冲突，有意新投递则用新 ID。接纳后才确认。
推理输入 ID 才证明消费，收到本身不证明。传输层/群组收件箱负责路由与重试；
不另建会话队列，不承诺跨会话事务或恰好一次执行。
延迟结果属于原 writer，而非选中 UI/分叉；源删除可能中断追溯，不影响本地内容保留。

群组名单/收件箱仍由外部管理。恢复须重新认领/协调提示，切换释放活跃成员关系，
分叉清除继承提示。回放不能重建进程/回调或子会话所有权；继续活跃子任务须重新验证。

**分叉。** 保护固定非空源前缀直到发布。用唯一子创建原位替换 seq 1；
保留源初始配置用于继承回放。后续事件按原序号复制，分配新的全局事件 ID，
保留原生消息 ID。重映射封装/载荷中的**全部**源本地事件引用，包含 source_id、
operation_ref、result_event_id 及 input/effect 引用；必需映射缺失则失败。
外部来源及本地序号锚点保持不变。

发布前独立复制资源并改写内容引用。历史委派不改变真实子会话父级、转移投递映射，
也不授予执行/权限/群组权威。继承回放后追加必要的子本地目标调整，group_hint
已设置时清空；不筛除历史补丁，不写空补丁。
整体发布暂存 transcript。从头回放须等于 Fork 结果，源删除后仍能解析全部内容；
历史中未完成的尝试不会重新启动。

**压缩。** 仅将有效上下文改为原生摘要 + 有序保留消息。
摘要进入 History 索引，无需另写消息事件。保留原历史、config/state 及资源；
存储无损不等于模型摘要无损。通过共享 Append/Compact 路径，生成在提交临界区外完成。

带审计生成先串行捕获输入并提交 inference.start，再将所得已提交前缀作为 source_seq，
之后派发。成功且接受时，**一个批次**依次提交 compaction.apply 和 inference.finish，
校验的是**批次前尾序号**。自身 finish 不使 apply 失效；任何中途写入使结果过期。
校验摘要身份、同会话已有且唯一的引用、原有效顺序及工具调用/结果一致性。
可保留先前仍有效摘要，不能留下悬空工具结果。

先确认是否已提交，再拒绝过期重试；同 ID/内容不新增，冲突失败。拒绝退役所有者结果。
失败/取消/无需压缩时上下文不变。过期被拒摘要仍可记录成功 inference.finish，
但不 apply 或保存未使用摘要正文。提交不明确时先核实，再写另一结果或重试。
所有压缩模式及连续压缩均使用此规则。

## JSONL 一致性与缓存

这些是**拟议要求**，不是普通 JSONL 追加已提供的保证。
初期覆盖本地文件，不覆盖网络文件系统。sdk-go 提供通用原生编解码、所有权及提交原语；
San 负责应用策略。

- 多行不原子，即使一次 write。Append/Compact 需要带版本、可验证的已提交批次边界，
  绑定身份、预期前尾、序号/数量及完整性。具体帧格式由 SDK 集成确定，不增加事件类型。
  先校验/准备投影，完整批次达到约定 flush/fsync 边界后才确认/发布；读者不见部分批次。
- 恢复只暴露经验证已提交前缀。忽略不完整尾批次，在独占下修复后才能继续写。
  已提交历史内部损坏须失败，不能跳过。以稳定 ID/内容核实不明确提交；取消不证明回滚。
  影响状态的未知版本阻止恢复。
- 采用带隔离机制的逐会话跨进程 writer 所有权，包含空闲句柄；不能仅靠时间戳租约，
  也不能 unlink 活跃锁来取得所有权。不跨模型、工具或审批持有提交临界区。
  进程死亡释放所有权，不证明外部操作完成；外部执行不保证恰好一次。
- 引用前发布并同步资源。Create/Fork 暂存已验证内容，只暴露完整发布，发现流程忽略暂存。
  备份一同保护已提交前缀、注册、权威控制/生命周期元数据、回收站及引用资源。
  分别复制正在变化的日志/blobs 不是一致备份。

| 数据 | 权威来源 / 缓存规则 |
| --- | --- |
| 项目身份/位置 | 经协调、带版本的内部目录；原子发布 |
| 会话身份及对话 | 创建及有序类型化事件；不另建并行 messages/state/settings 存储 |
| 恢复活动及回收站生命周期 | 权威版本化控制 sidecar / 生命周期元数据，纳入备份 |
| `.meta`、定位器及索引 | 可重建；验证格式/投影器、已提交前缀及相关控制/生命周期修订 |
| 可选检查点 | 暂缓至实测证明收益；不含秘密/活跃资源；无效缓存回退到事件 |

缓存崩溃后可落后，不能领先于已提交历史、隐藏已提交会话或复活已删除会话。
一致读取前验证/重建，跨进程协调失效。缓存原子发布，内存在事件提交后发布；
不声称物理跨文件原子性。全局事件 ID 冲突/重试校验须覆盖跨分区及导入，
随机分配本身不等于校验。文件格式、编解码和投影器版本分开；不预设推测性缓存模式。

## 保留与安全清理

默认：**不自动删除正常历史**。近期筛选、归档、固定、可恢复 Delete 和永久 Clean
互不相同；压缩不属于任何一种。

| 策略 | 默认值 / 效果 |
| --- | --- |
| 近期列表 | 30 天，只筛选展示。固定会话绕过年龄但不绕过显式归档筛选；未知活动保持可见。 |
| 固定 | false；设置后阻止手动/批量 Delete 及自动保留清理。须显式取消固定，无隐含强制选项。 |
| 回收站 | 软删除后 7 天；确认前显示期限。Delete 授权后续到期清理，不能提前永久清除。 |
| 自动保留清理 | 禁用。显式设置不活动年龄（如 90 天）后将符合条件的正常/归档会话移入回收站，并授权后续到期清理，不直接永久清除。 |

旧正常历史可通过 activity=all、适当归档筛选、Get/Events 及显式 resume 找到。
回收站默认隐藏，visibility=trash/all 列出时无年龄限制。Get 返回墓碑状态；
Events 需 include_trash。Open/Fork/Append/Archive 拒绝回收站内容，须先 Restore。
读取不静默恢复。

活动使用可信 `updated_at`：有意义的已接纳写入，或通过所有权/投影/策略检查的
成功显式 resume。Resume 在小型版本化控制 sidecar 记录 last_resumed_at，不写事件。
单独 Open、读取、重试/无变化、扫描、缓存重建、备份、Restore 和清理不推进活动。
导入时间戳、mtime 及发现时间不是当前活动；旧活动未知则保持未知、可见且不自动清理。

共享 config_root 的既有配置拥有保留策略。本地覆盖只能禁用清理或延长保护。
策略缺失、不可信或有歧义则阻止破坏性清理；变更不能静默缩短已有回收站期限。
这些默认值是拟议逻辑策略，不是已实现设置键或 CLI 参数。

Clean 限于 Store 项目且有界；IDs 只缩小范围，不沿父子/分叉链接扩展。
DryRun 不写入、不修复、不认领租约；需恢复时报告阻塞。结果包括逐 ID 动作/原因、
可恢复性/期限、验证过的字节估算（否则未知）、截断及取消/失败时部分结果。

确认预览仅传选中的返回 IDs。预览不预留或授权；执行在与 writer 相同互斥下重新检查
当前策略、前缀/控制/生命周期修订、墓碑及所有者。变化候选跳过/报告，
不扩展范围或替换候选。定时清理有自己的策略授权；持久重试代次保持内部实现，
不要求客户端计划令牌、回执或操作 ID。

保护固定会话、空闲 writer、后台工作、待投递/审批、未完成或未知执行/活动、
损坏及不支持模式。协调 fork/import/backup 和投递保护；无法安全隔离则跳过。
崩溃工作先核实再判断年龄。活跃依赖影响安全；父子关系不是永久保留或级联规则。
Delete/Restore 也执行适用保护，不承诺子树事务。

回收站位于稳定项目分区，与日志/资源同一文件系统，不在 workspace/cwd 下。
权威生命周期元数据保留身份、清单、验证前缀、授权/期限及重启进度。
移动前隔离发现/Open/旧句柄。**多文件移动不原子**：未完成转换保持受保护，
直到恢复或安全回滚；读者不见部分历史。

Restore 验证保留内容/引用及原关联。缺失/损坏或已开始永久清除则失败；
ID/路径冲突不能覆盖、合并或分配新 ID。完整发布原身份/历史/归档/固定状态，
不授予 runtime 权威。重试区分已恢复代次与后续删除。
只有 Clean 永久删除到期回收站：先持久标记不可用，再可重启地回收已验证所属文件，
不复活数据，并保留生命周期依据直到完成。

缓存驱逐协调读写。资源 GC 扫描**全部保留历史**，包含压缩前、归档/固定及回收站，
保护暂存/发布/备份。只有已验证临时/孤儿文件经过宽限期（如 24 小时）才符合条件，
unlink 前复查引用。扫描不完整、所有者/格式未知或引用歧义则跳过。
日志、引用资源及活跃租约文件不是垃圾。

交互就绪后/空闲时调度有界清理。CLI 不运行就没有守护进程，到期只是下次安全运行的资格。
磁盘压力仅提示、预览/导出及提供显式选择，不缩短期限或绕过保护。

## 落地与验证

1. 移除 SQLite 实验代码和依赖，保留文件元数据、显式同步、读取锁优化及对应测试。
   保持当前原生编解码/truncate 行为；不引入后端开关。
2. 确定 SDK 对齐的批次帧、所有权、恢复和缓存验证。对 write/sync/rename/unlink
   边界、部分批次/移动、不明确提交及备份进行故障测试。
3. 在文件上实现注册、类型化适配及投影，测试 TUI/无界面/ACP、hooks/inspector，
   区分摘要、事件/历史浏览和恢复状态三条读取路径。
4. 转换须获准、独占、可重试，保留原件/资源、来源及新旧投影等价证明。
   不支持记录阻止切换，不静默合并分区或双写；新格式写入后回滚前保留/导出新历史。
5. 先测量再优化：增量摘要、缩小锁、回放分配及可选检查点。
   后端测试通过本身不能证明目标契约。

验证覆盖身份/worktree/配置隔离及信任、18 类事件无回放副作用、原生/提供方字段往返、
重试与有意重复、委派/分叉独立性及延迟回调、连续/审计/过期压缩、固定前缀浏览、
当前策略恢复、保留默认值/固定/未知保护、预览竞态、中断的删除/恢复/清除、GC 及一致备份。

剩余实现选择：位置迁移/协调交互、歧义来源选择、注册文件格式、最终摘要/载荷类型、
SDK 批次集成及转换记账。Git common-directory 身份、非 Git 精确 cwd 和原始主检出
config_root 已确定。SQLite 或文件上的 SQLite 索引须另行决策并提供新证据。

## 历史性能 — 2026-11-07

以下是暂缓实验的测量，不是目标保证或本次新测试结果。
Apple M5 / 32 GiB、darwin/arm64、Go 1.26.9、modernc.org/sqlite v1.44.3、GOMAXPROCS=4。
数值为三次基准运行平均值的中位数，不是 P95/P99 或冷磁盘延迟。
不计样本准备，未清 OS 缓存。JSONL 追加显式 Sync，有变化的 Save 已自行同步。
SQLite 每连接使用 WAL、synchronous=FULL、fullfsync=ON 和 checkpoint_fullfsync=ON，
匹配 Go 在 macOS 上的 F_FULLFSYNC 边界。未启用这些设置的早期测量已排除。

| 场景 | JSONL ms/op | SQLite ms/op | 解释 |
| --- | ---: | ---: | --- |
| San 持久应用事件追加 | 3.993 | 4.043 | 接近；SQL 分配量更多 |
| 100 条上下文后追加原生消息 | 4.007 | 4.953 | 约慢 24% |
| San 回放 1,000 条消息 | 2.947 | 3.534 | 约慢 20% |
| 列出 100 个会话，摘要热缓存 | 0.225 | 0.065 | 约快 3.5 倍 |
| 内存缓存丢失，JSONL 磁盘摘要仍存在 | 1.490 | 37.900 | 约慢 25 倍；SQLite 尚无持久化产品摘要 |
| 全部摘要失效后列出会话 | 44.386 | 37.271 | 约快 16% |
| San Fork，100 条消息 | 408.127 | 438.396 | 约慢 7%；当前逐记录复制协议 |
| SDK 独立持久写入，4 个 worker | 3.110 | 5.465 | 聚合每操作耗时约差 76%；单数据库 writer 竞争 |

列表每会话 100 条消息。回放 1,000 条时分配约 2.27 MB/op（JSONL）与 3.80 MB/op（SQLite）。
相同参数去符号二进制分别为 **82,744,498 与 86,546,162 字节**，增加约 3.80 MB，
即使运行时选择 JSONL 也存在。

WITHOUT ROWID 在 1,000 条消息、256 字节/8 KiB 样本回放约快 5–8%，
但 100 × 1 KiB 追加样本检查点后数据库为 487,424 与 159,744 字节；
活跃 DB/WAL/SHM 合计为 2,195,776 与 1,577,776 字节。没有普遍占优的布局。

历史结论：保持 JSONL。持久产品摘要、减少逐记录 Fork 提交、回放分配及 writer 竞争仍需优化。
列表已有独立摘要锁，只读历史不获取 writer 缓存锁；其余 writer/投影操作仍全局串行。
SDK Save/Fork 原子，而 San 复合压缩/Fork 仍使用现有多调用协议。

未来比较须使用相同原生样本/资源及持久边界，区分冷/热摘要、浏览和恢复，
报告批量/并发 writer 下的分布、分配量、体积和竞争。现有基准不证明自动导入、
目标 ID 转换、检查点、完整 runtime 集成或断电认证。
