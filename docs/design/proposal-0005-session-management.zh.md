# PROP-0005: 会话管理与文件持久化

[English](proposal-0005-session-management.md)

## 状态与范围

草案 — 2026-11-07。本 PR 仅评审设计：以文件/JSONL 实现存储中立的会话/事件 API。
不采用 SQLite；历史测量仅作为依据，不引入数据库迁移或后端选择 UI。
配套[实现 PR #737](https://github.com/genai-io/san/pull/737) 尚未合入；实现状态指该分支，不是本 PR 或主分支新增能力。
第一阶段包括 `OpenStore` 注册、Git worktree 共享会话归属、当前检出项目设置、非 Git 精确路径归属，以及 schema 4 可选身份字段/摘要缓存升级；该分支正按本次配置决定调整，尚不代表最终行为已验证。旧 cwd 分区不自动迁移。
仍使用 SDK 原生记录及 transcript/truncate 语义；18 类事件、完整目标 API、原子批次、回收站和自动保留策略尚未实现。Hooks/inspector 保持 JSONL 集成。
新格式切换须先补齐 SDK 原子批次及可阻断执行的持久化接纳边界，不能只重命名事件。
每项事实只有一个权威来源：持久化对话变化、必要执行/权限审计及不可重建说明，而非通用 runtime 日志；摘要/索引必须可重建。

## 身份、目录与配置

```text
checkout A --+
             +-- Git common directory --> one Project --> shared sessions
checkout B --+                              (workspace = A or B)

checkout A --> A's project config / tracked files (branch A)
checkout B --> B's project config / tracked files (branch B)
user scope --> naturally shared user-global config
```

Workspace 是路径属性，不是实体、服务或目录层。Session 是可恢复对话，不是进程/agent 运行；Close 释放句柄，不终结会话。初期没有 `session.end`。
Runtime 负责执行/取消；持久待办、权限决定及群组提示不是活跃资源。项目使用注册时分配的稳定不透明 ID，与路径查询键分开：

| 启动位置 | 内部注册规则 |
| --- | --- |
| Git | 规范化绝对 Git common directory；先按 Git 定义基准解析相对结果。主检出、linked worktree 及子目录共享 Project；独立 clone/submodule 独立。 |
| 非 Git | 精确规范化启动 cwd 就是查询键、项目根及 workspace。同路径复用注册；子目录启动属于另一 Project，不查最近祖先。 |
| 两者 | 绝对路径/符号链接规范化与信任检查一致，不转小写；运行时 cwd 变化不重新分配归属。 |

`project_id`/`workspace` 固定于创建；Git workspace 是检出根，`initial_cwd` 可在其下。可变 `execution_cwd` 可跨项目，不迁移会话。
从状态恢复执行位置，旧历史回退到初始 cwd；按执行位置解析工具、配置、Hook 和信任。
移动、路径复用及 Git 元数据缺失/变化须显式验证并协调/选择注册；不静默转移身份、合并旧分区或改写路径。关联不授予信任；检出缺失不妨碍历史查阅。

共享 Project 身份/会话**不意味着集中配置**：
- Git 项目设置取**当前检出根**，不是原始主检出；非 Git 取精确启动 cwd。Git 跟踪的设置、指令等随正常 checkout 切换，各分支可有意不同。
- 不要求共享配置根，不自动复制/同步或回退其他检出。会话管理不要求主检出存在。用户全局配置自然共享；显式链接仅为自愿选择。
- 不为共享身份/会话重构配置、MCP、插件、技能或指令加载器；保留各自路径规则，不声称所有加载器已把子目录归一到检出根。适用的旧本地来源/信任兼容行为可保留，不是一概废除旧回退。

保留用户 -> 项目 -> 本地解析、适用兼容来源、persona 及逐字段合并；见[配置](../reference/configuration.md)。写入只选一个作用域并更新其字段，跨进程协调、拒绝只读来源，不写回合并结果；保留逐字段来源。
注册锁等待目前不能中途取消，原子发布不承诺断电持久性；路径完全相同的仓库替换无法自动识别。
会话覆盖只影响后续请求；恢复使用当前策略/凭据，不复用历史授权。模型/提供方缺失须报错或明确替代；配置文件与事件无跨文件事务，不把秘密、安全权威或完整设置树复制到历史。

旧存储按精确 cwd 哈希分区，新注册使用稳定项目 ID。`~/.san/projects/<project-id>/` 下：
`project.json` 保存身份/查询元数据而非配置；`transcripts/<sid>.jsonl` 是日志，`.jsonl.meta` 是可重建摘要，`blobs/{image,tool-result}/<sid>/` 保存会话资源；拟议 `trash/<sid>/` 保留删除代次、清单、日志及资源。
`project.json` 已用于新注册，回收站仍属拟议。扫描注册可重建查询索引，不增加身份权威；旧 `<cwd-hash>` 不自动改名/移动。
Worktree 共用分区，以 Session.workspace 区分；父子日志同级，以 parent_session_id 关联。配置仍在当前检出/用户作用域，由原加载器管理，不复制到会话分区；回收站转换不能假设多文件移动原子。
内部目录负责项目名称/描述、注册及分区位置，不负责配置/当前 cwd。已有分区在获准协调前保持独立可读，不隐含导入。
有会话、回收站或未完成转换时禁止移除项目。删除会话不删除检出；父子关系不意味着级联删除。

## 事件契约

Event 是不可变持久事实，不是临时通知或重命名的 `transcript.Record`。`<object>.<action>` 中 apply 表示接受转换，finish 表示终止结果，不一定成功。

| 封装字段 | 类型及含义 |
| --- | --- |
| `id` | 全局唯一字符串，提交前分配；持久化重试保持不变 |
| `session_id` | 归属会话 ID；项目/workspace 由会话获取 |
| `seq` | 连续正整数会话内提交顺序，提交时分配 |
| `type`, `schema_version` | 事件类型及正整数载荷编解码版本 |
| `created_at` | UTC Unix 毫秒，不是排序键 |
| `source_id?` | 本地或外部直接来源**事件 ID**，不是会话、请求、原生消息 ID 或旧序号 |
| `outcome?` | 仅 inference/tool/delegation/hook finish 必填：success/failure/cancelled |
| `payload` | 经验证的类型化 JSON，不重复封装字段 |

Start ID 就是操作身份。Finish 的 `source_id` 指向 start；permission.decide 指向 request。生成消息/摘要指向生产方 start；接收消息指向源内容，委派输入指向 delegation.start；直接用户输入可无来源。
原生 call_id/turn_id 留在载荷。来源表示溯源，不是所有权/完整依赖图。下表 `?` 可选，其余必填；原生消息、工具定义及嵌套状态保留 SDK/产品类型，不简化为字符串。具体 Go 载荷类型仍须 SDK 对齐。

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

ConfigPatch 允许 provider、model、generation_options、mode、autopilot_policy；参数/策略使用版本化白名单产品字段，不接受任意提供方 JSON。
StatePatch 允许 title、archived_at、pinned、todos、autopilot_progress、execution_cwd、group_hint；提示只含重新认领用群组/成员身份，不是名单/权限权威。最近提示词预览派生。
补丁区分未提供、设置、清空；false/零/空为显式值。拒绝未知/跨归属字段。具名变更要求名称唯一、upsert/remove 不相交、请求摘要可解析；空目录为已知值，未变化补丁/定义不产生事件。

校验及记录规则：
- 创建唯一且为 seq 1；父子无环。分叉源 ID/序号同时缺省或指向非空源前缀，初始配置不含秘密。
- 原生消息 ID 非空且本地唯一；冲突失败。保留签名、加密推理、图像/内容顺序、工具 ID 及支持的提供方字段。当前 Record.Message/Messages 为 `json:"-"`，直接序列化 Record 会丢失内容。
- 消息批次只有一个来源，混合须拆批；来源不可变，不按相邻位置/时间推测。本地引用解析到已提交前缀或同批更早已验证项；input_ref 指向不可变原生调用/已持久调用内容，必需内容缺失则失败。
- Finish 匹配同会话 start/类别及工具 call ID；每 start/request 至多一个终止结果。失败需有界结构化错误，取消需原因；权限作用域不扩大请求。未知保持未知，派发确认不是 finish。
- 可信适配层接纳时验证外部来源；回放仅读本地内容，不依赖外部源存续。导入来源不可用须显式兼容处理，不能伪造引用。
- 派发前提交 start，持久化失败则不执行；输出/finish 同时已知时合批。较早完整输出可在崩溃后保留而无已知 finish；回放不重试或产生副作用。
- 同事件 ID/内容幂等，冲突复用失败；新执行/有意重复输入使用新 ID，即使文本相同。不保存 token 增量、心跳、进度刷新、空补丁、重复快照、可派生 UI 文案、秘密或无界诊断。渲染器只消费事实。

```text
runtime adapters --> validate + commit batch --> committed Events (seq)
                         |                          |
                    failure: no dispatch            +--> pure projector
                    start committed: dispatch              |-- History (full)
                                                          |-- Snapshot (active)
                                                          `-- Summary (list)
replay --------------------------------------------> projector only; no effects
```

回放只投影已提交事实，不调用模型/工具/Hook，不弹授权、重投或追加事件。提交后由实时协调层处理环境变化；恢复只解析一次最终配置/信任。

## 当前记录 → 目标事件

当前存储结合 SDK message/truncate/应用记录与有 **19 个类型常量**的 San 模式 `"4"`，不是这 18 类事件。
见 [records.go](../../internal/transcript/records.go)、[read.go](../../internal/transcript/read.go)；当前读取 ID 是日志位置。下表为语义适配提案，不是改名迁移。

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

缺失 start、全局 ID、关联/结果须显式兼容映射，不丢弃或伪造。无法无损映射则阻止导入/切换并保留原件；须证明新旧有效上下文、完整历史、原生 ID、资源、inspector 来源等价，包括旧替换语义。

## API

公开模型是具体的项目作用域 `*Store` 和持有所有权的 `*Session`；不公开 Projects、Settings、Workspaces、SessionStore、EventReader 或 History 服务。使用方可定义窄接口，最终类型/原语须 SDK 对齐。
当前 internal/session.Store 有 Create、Open、List/ListContext、Fork、Delete、Close；Open/Fork 还返回 History。Session.Append 接收单个 transcript.Record，Session.Close 已存在；transcript.Store.Records 提供原始记录，Delete 立即删除历史。以下签名及可恢复 Delete 均为拟议。

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

OpenStore 规范化启动 dir，内部注册/解析项目并固定作用域；除显式 Fork 源外，所有 ID 须属于该项目。I/O 支持 context 取消；Snapshot 读取不可变内存已提交投影。

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

List 按 `(updated_at, id)` 实时键集排序，不是跨请求快照；未知活动置于按 ID 排序的稳定分组。History 从完整固定前缀投影后才对展示分页，不能用孤立原始页/筛选流重建，不能从 UI 历史恢复执行。
未来完整历史检查点可加速；仅有效上下文缓存不能替代 History。

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

恢复：Open -> 投影 -> 当前配置/信任 -> runtime。TUI/无界面/ACP 共用所有权规则；ACP 构建服务前核对请求 cwd/恢复执行位置，不符须显式获准变更。
打开失败释放所有权，切换失败保留原选择；区分不存在、忙碌、关闭、冲突、无效、不支持模式、损坏、取消及存储错误。

## 子 agent、分叉与压缩

```text
parent: delegation.start D -- dispatch --> child: independent event stream
        (reserve child ID)                           |
        [message.append R + delegation.finish] <----+ accepted result
         R.source_id = child content event           (copy content/assets)
         finish.source_id = D; result_event_id = R
```

**子 agent。** 独立事件流。delegation.start 前预留子 ID；派发前创建/认领并验证父级/任务/子会话所有权。每次继续执行用新 start ID。
父会话只存协调/接纳内容，不转存子事件流。delegation.finish 的 source_id 指 start，result_event_id 指本地接纳结果；已知结果 + finish 合批，不虚构空输出。
跨会话接纳独立保存内容/资源，用新接收方本地原生 ID 及对齐 source_message_ids，保留提供方字段/工具身份。可信投递层跨重试/重启保留稳定接纳事件/消息 ID；内容/来源/接收方变化即冲突，有意新投递用新 ID，接纳后才确认。
推理输入 ID 才证明消费，收到不证明。传输层/群组收件箱负责路由/重试，不另建会话队列，不承诺跨会话事务或恰好一次执行。
延迟结果属于原 writer，不是选中 UI/分叉；源删除可中断追溯但不影响本地内容。群组名单/收件箱由外部管理，恢复重新认领/协调提示，切换释放成员关系，分叉清除提示。
回放不重建进程、回调或子会话所有权；继续活跃子任务须重新验证。

**分叉。** 保护固定非空源前缀至发布；唯一子创建原位替换 seq 1，保留源初始配置供继承回放。后续事件按原序号复制，使用新全局事件 ID，保留原生消息 ID。
重映射封装/载荷中**全部**源本地事件引用，包括 source_id、operation_ref、result_event_id 及 input/effect 引用；必需映射缺失则失败。外部来源及本地序号锚点不变。
发布前独立复制资源/改写引用。历史委派不改变真实子父级、转移投递映射或授予执行/权限/群组权威。继承回放后追加必要的子本地目标调整，清空已设置 group_hint；不筛除历史补丁、不写空补丁。
整体发布暂存 transcript；从头回放等于 Fork 结果，源删除后仍能解析全部内容；历史未完成尝试不重启。

**压缩。** 仅把有效上下文改为原生摘要 + 有序保留消息；摘要进入 History 索引，无需另一消息事件。保留原历史、config/state/资源，存储无损不等于模型摘要无损；生成在提交临界区外完成，共用 Append/Compact 路径。
带审计生成先串行捕获输入/提交 inference.start，再捕获已提交 source_seq，之后派发。接受成功时一个批次依次提交 apply + finish，校验**批次前尾序号**；自身 finish 不使 apply 失效，中途任何写入使结果过期。
验证摘要身份、同会话已有唯一引用、原有效顺序、工具调用/结果一致性；可保留先前仍有效摘要，不留悬空工具结果。

```text
commit inference.start --> capture source_seq S --> generate outside commit lock
                                                        |
                    retry: check prior commitment <-----+
                    owner valid + pre-batch tail == S?
                      yes --> [compaction.apply + inference.finish] atomic batch
                      no  --> no apply; context unchanged (finish may still be known)
```

先查已提交再拒绝过期重试；同 ID/内容不新增，冲突失败，拒绝退役所有者结果。失败/取消/无需压缩则上下文不变。
过期被拒摘要可有成功 inference.finish，但不 apply 或保存未用摘要正文；提交不明先核实再写另一结果/重试。所有模式及连续压缩均遵守。

## JSONL 一致性与缓存

以下为**拟议要求**，不是普通 JSONL 追加现有保证。初期覆盖本地文件而非网络文件系统；SDK 提供通用原生编解码、所有权/提交原语，San 负责策略。
- 多行不原子，即使一次 write。批次边界须版本化、可验证，绑定身份、预期前尾、序号/数量/完整性；帧格式由 SDK 集成确定，不增事件类型。先校验/准备投影，完整批次达到约定 flush/fsync 后才确认/发布，读者不见部分批次。
- 恢复只暴露已验证提交前缀；忽略不完整尾批次，独占修复后再写。已提交历史内部损坏须失败，不跳过。稳定 ID/内容核实不明确提交，取消不证明回滚；影响状态的未知版本阻止恢复。
- 逐会话跨进程 writer 所有权须带隔离，包含空闲句柄；不能仅靠时间戳租约或 unlink 活跃锁抢占。不跨模型/工具/审批持有提交临界区；进程死亡释放所有权，不证明外部操作完成，外部执行不保证恰好一次。
- 引用前发布/同步资源；Create/Fork 暂存验证内容，只暴露完整发布，发现忽略暂存。备份一起保护提交前缀、注册、权威控制/生命周期元数据、回收站/引用资源；分别复制变化中的日志/blobs 不是一致备份。

| 数据 | 权威来源 / 缓存规则 |
| --- | --- |
| 项目身份/位置 | 经协调、带版本的内部目录；原子发布 |
| 会话身份及对话 | 创建及有序类型化事件；不另建并行 messages/state/settings 存储 |
| 恢复活动及回收站生命周期 | 权威版本化控制 sidecar / 生命周期元数据，纳入备份 |
| `.meta`、定位器及索引 | 可重建；验证格式/投影器、已提交前缀及相关控制/生命周期修订 |
| 可选检查点 | 暂缓至实测证明收益；不含秘密/活跃资源；无效缓存回退到事件 |

缓存可落后但不能领先提交历史、隐藏会话或复活已删除会话；一致读前验证/重建，跨进程协调失效。缓存原子发布，内存在事件提交后发布，不声称物理跨文件原子性。
全局事件 ID 冲突/重试校验覆盖跨分区及导入，随机分配不等于校验。文件格式、编解码、投影器版本分开，不预设推测性缓存模式。

## 保留与安全清理

默认**不自动删除正常历史**。近期筛选、归档、固定、可恢复 Delete、永久 Clean 互不相同；压缩不属任何一种。

| 策略 | 默认值 / 效果 |
| --- | --- |
| 近期列表 | 30 天，只筛选展示。固定绕过年龄但不绕过显式归档筛选；未知活动可见。 |
| 固定 | false；设置后阻止手动/批量 Delete 及自动保留清理。须显式取消固定，无隐含强制选项。 |
| 回收站 | 软删除后 7 天；确认前显示期限。Delete 授权后续到期清理，不能提前永久清除。 |
| 自动保留清理 | 禁用。显式设置不活动年龄（如 90 天）后将符合条件的正常/归档会话移入回收站，并授权后续到期清理，不直接永久清除。 |

旧历史经 activity=all、适当归档筛选、Get/Events/显式 resume 仍可达。回收站默认隐藏，visibility=trash/all 无年龄过滤；Get 返回墓碑，Events 需 include_trash。Open/Fork/Append/Archive 拒绝回收站内容，须先 Restore；读取不静默恢复。
活动使用可信 `updated_at`：有意义的接纳写入或通过所有权/投影/策略检查的成功显式 resume。后者在版本化控制 sidecar 记 last_resumed_at，不写事件。
单独 Open、读取、重试/无变化、扫描、缓存重建、备份、Restore/清理不推进活动；导入时间戳、mtime、发现时间不算当前活动。旧活动未知则保持未知、可见且不自动清理。

```text
live/archived -- Delete or authorized retention --> trash -- deadline --> expired trash
     ^                                               |                       |
     +-------------------- Restore ------------------+                 safe Clean
                                                                         |
                                                                   permanently removed
pin / owner / pending work / unknown state --> block destructive transitions
```

检出本地的保留策略不能意外删除另一 workspace 会话。自动清理限于 OpenStore 捕获的当前 workspace，除非有**显式项目级授权**。可使用用户全局策略，但共享来源本身不授权扩大范围；复用现有配置/授权边界，不增加策略服务。
策略缺失、不可信/有歧义则阻止破坏性清理；变更不静默缩短已有回收站期限。这些是拟议规则，不是已实现设置键/CLI 参数。
Clean 有界且限于 Store 项目及获准 workspace 范围；IDs 只缩小范围，不授予项目级权限、不沿父子/分叉链接扩展。显式项目级清理须另有授权，更宽 List 结果不构成授权。
DryRun 不写、不修复、不认领租约，需恢复时报告阻塞；结果包含逐 ID 动作/原因、可恢复性/期限、验证字节估算（否则未知）、截断及取消/失败部分结果。
确认预览只传选定返回 IDs；预览不预留/授权。执行与 writer 同互斥下复查当前策略、前缀/控制/生命周期修订、墓碑/所有者；变化候选跳过/报告，不扩展或替换候选。
定时清理有自己的策略授权；持久重试代次留在内部，不要求客户端计划令牌、回执或操作 ID。
保护固定、空闲 writer、后台工作、待投递/审批、未完成/未知执行或活动、损坏及未知模式；协调 fork/import/backup/投递保护，无安全隔离则跳过。崩溃工作先核实再判断年龄，活跃依赖影响安全，父子不是永久保留或级联规则；Delete/Restore 同样执行适用保护，无子树事务。
回收站在稳定项目分区，与日志/资源同文件系统，不在 workspace/cwd 下；生命周期元数据保留身份、清单、验证前缀、授权/期限及重启进度。移动前隔离发现/Open/旧句柄；**多文件移动不原子**，未完成转换受保护至恢复/安全回滚，读者不见部分历史。
Restore 验证内容/引用及原关联；缺失、损坏或 purge 已开始则失败。ID/路径冲突不覆盖/合并/另分 ID；完整发布原身份/历史/归档/固定状态，不授予 runtime 权威。重试区分已恢复代次与后续删除。
只有 Clean 永久删除到期回收站：先持久标记不可用，再可重启回收经验证所属文件，不复活数据；生命周期依据保留至完成。
缓存驱逐协调读写；资源 GC 扫描**全部保留历史**，含压缩前、归档/固定及回收站，保护暂存/发布/备份。仅已验证临时/孤儿文件过宽限期（如 24 小时）才可删除，unlink 前复查引用；扫描不全、未知所有者/格式/歧义引用则跳过，日志、引用资源、活跃租约不是垃圾。
交互就绪后/空闲时有界调度；CLI 不运行则无守护进程，到期只是下次安全运行资格。磁盘压力只提示、预览/导出及显式选择，不缩短期限或绕过保护。

## 落地与验证

1. 移除 SQLite 实验/依赖，保留文件元数据、显式同步、读取锁优化/测试；保持原生编解码/truncate，无后端开关。
2. 确定 SDK 对齐的批次帧、所有权、恢复/缓存校验；故障测试 write/sync/rename/unlink 边界、部分批次/移动、不明确提交及备份。
3. 文件上实现注册、类型化适配/投影；测试 TUI/无界面/ACP、hooks/inspector，区分摘要、事件/历史浏览、恢复状态三条读路径。
4. 转换须获准、独占、可重试，保留原件/资源/来源及新旧投影等价证明；不支持记录阻止切换，不静默合并分区/双写；新格式写入后回滚须先保留/导出新历史。
5. 先测量再优化增量摘要、锁、回放分配/可选检查点；后端测试通过不证明目标契约。

验证覆盖身份/worktree/配置隔离及信任、18 类事件无回放副作用、原生/提供方往返、重试/有意重复、委派/分叉独立性及延迟回调、连续/审计/过期压缩、固定前缀浏览、当前策略恢复、保留默认/固定/未知保护、预览竞态、中断删除/恢复/清除、GC/一致备份。
剩余选择：位置迁移/协调交互、注册文件格式、最终摘要/载荷类型、SDK 批次集成及转换记账。Git common-directory 身份、非 Git 精确 cwd、当前检出项目配置已确定；SQLite 或文件上的 SQLite 索引须另决策并提供新证据。

## 历史性能 — 2026-11-07

以下为暂缓实验测量，不是目标保证或本次新测试。Apple M5 / 32 GiB、darwin/arm64、Go 1.26.9、modernc.org/sqlite v1.44.3、GOMAXPROCS=4。
数值为三次运行平均值的中位数，不是 P95/P99 或冷磁盘延迟；不计样本准备，未清 OS 缓存。JSONL 追加显式 Sync，有变化 Save 已同步。
SQLite 每连接 WAL、synchronous=FULL、fullfsync=ON、checkpoint_fullfsync=ON，匹配 Go macOS F_FULLFSYNC 边界；排除未启用设置的早期测量。

| 场景 | JSONL ms/op | SQLite ms/op | 解释 |
| --- | ---: | ---: | --- |
| San 持久应用事件追加 | 3.993 | 4.043 | 接近；SQL 分配量更多 |
| 100 条上下文后追加原生消息 | 4.007 | 4.953 | 约慢 24% |
| San 回放 1,000 条消息 | 2.947 | 3.534 | 约慢 20% |
| 列出 100 个会话，摘要热缓存 | 0.225 | 0.065 | 约快 3.5 倍 |
| 内存缓存丢失，JSONL 磁盘摘要仍存在 | 1.490 | 37.900 | 约慢 25 倍；SQLite 尚无持久化产品摘要 |
| 全部摘要失效后列出会话 | 44.386 | 37.271 | 约快 16% |

列表每会话 100 条。回放 1,000 条分配约 2.27 MB/op（JSONL）与 3.80 MB/op（SQLite）；同参数去符号二进制 **82,744,498 与 86,546,162 字节**，即使运行时选 JSONL 也增加约 3.80 MB。
历史结论：保持 JSONL，不是普遍更快。持久摘要支持不对等限制列表比较；先优化摘要、回放分配、逐记录提交。SDK Save/Fork 原子，San 复合压缩/Fork 仍为现有多调用协议。
未来比较须同原生样本/资源/持久边界，区分冷/热摘要、浏览/恢复，报告批量/并发 writer 的分布、分配、体积、竞争。现有基准不证明自动导入、目标 ID 转换、检查点、完整 runtime 集成或断电认证。
