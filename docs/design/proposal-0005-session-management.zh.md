# PROP-0005: 会话管理与文件持久化

[English](proposal-0005-session-management.md)

## 状态与范围

草案 — 2026-11-07，仅评审文件/JSONL 存储中立 API，不引入 SQLite 迁移或后端选择 UI。每项事实一个权威：对话变化、必要执行/权限审计、不可重建说明；不是 runtime 日志，摘要/索引可重建。

| 交付 | 状态 |
| --- | --- |
| [实现 #737](https://github.com/genai-io/san/pull/737) | 未合入：项目/worktree 身份、当前检出配置、schema 4 身份/缓存升级；18 类类型化事件及纯投影器是尚未接入的基础代码。 |
| [SDK #105](https://github.com/genai-io/sdk-go/pull/105) | 未合入：可选批次提交及推理/工具/输出接纳边界。完整行可见不证明 Sync 已完成；Commit 成功才确认文件同步。 |
| 现有持久化 | SDK 原生记录/truncate；Hooks/inspector 保持 JSONL；不迁移旧分区、不双写。 |
| 下述目标 | 新事件写入、完整生产方接入/API、回收站及保留仍待完成；基础代码不等于格式切换。 |

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

```text
creation: project_id + workspace + initial_cwd (fixed)
                                  |
resume: state.execution_cwd -------+ fallback for old history
                  |
                  +--> tools / settings / hooks / trust at execution location
                  `--> cwd may cross projects; ownership NEVER follows
```
Git workspace = 检出根，initial_cwd 可在其下。移动、路径复用、Git 元数据缺失/变化须显式验证并协调注册；不静默转移身份、合并旧分区或改写历史。关联不授予信任，检出缺失不妨碍查阅。

| 配置边界 | 规则 |
| --- | --- |
| 项目设置 | 当前 Git 检出根，不是原始主检出；非 Git 为精确启动 cwd。跟踪设置/指令随 checkout/分支变化。 |
| 共享 | 用户全局自然共享，链接自愿；无强制共享根、复制/同步或其他检出回退，不要求主检出存在。 |
| 旧加载器 | 配置/MCP/插件/技能/指令保留路径规则及适用旧本地来源/信任兼容；共享身份无需重构，也非所有加载器均按检出根解析子目录。 |
| 解析/写入 | 保留用户 → 项目 → 本地、兼容来源、persona、逐字段合并/来源（[配置](../reference/configuration.md)）。只写所选作用域字段，不写合并结果；跨进程协调，拒绝只读来源。 |
| 会话覆盖/恢复 | 只影响后续请求；使用当前策略/凭据，不恢复历史授权。模型缺失报错或明确替代；无配置/事件事务，不复制秘密、安全权威或完整设置树。 |
| 当前限制 | 注册锁等待不能中途取消；原子发布 ≠ 断电持久；同路径仓库替换不能自动识别。 |

```text
~/.san/projects/
  <cwd-hash>/                      legacy: separate, readable; no auto-move/import
  <project-id>/                   shared worktrees; no workspace directory layer
    project.json                  identity/lookup metadata, NOT config (implemented)
    transcripts/<sid>.jsonl        peer parent/child logs; parent_session_id links
    transcripts/<sid>.jsonl.meta   rebuildable summary
    blobs/{image,tool-result}/<sid>/
    trash/<sid>/                  PROPOSED generations/manifests/logs/assets
checkout / user scope             config stays with existing loaders
```
内部目录负责名称/描述、注册/分区位置，不负责配置/cwd；扫描只重建查询索引。协调须获准；会话、回收站或未完成转换阻止移除项目。会话删除不删除检出或按祖先级联；回收站恢复不能假设多文件移动原子。

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

```text
OpenStore(dir) --> project-scoped *Store (internal registration; no public services)
  |-- Get/List ------------> summaries; no writer claim
  |-- Events --------------> fixed committed prefix --> History --> display pages
  |-- Create/Open/Fork ----> exclusive *Session
  |                            |-- Append/Compact --> atomic commit
  |                            |-- Snapshot -------> active committed projection
  |                            `-- Close ----------> drain + release (not session end)
  `-- Delete/Restore/Clean -> lifecycle metadata, not conversation events
```
使用方可定义窄接口；最终原语/类型对齐 SDK。**下列签名属拟议**：当前 Store 有 Create/Open/List/ListContext/Fork/Delete/Close；Open/Fork 还返回 History，Append 接收单个 transcript.Record，Records 暴露原始记录，Delete 立即删历史，Session.Close 已存在。

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

持久写入：Create/Fork → 唯一 session.create + 重映射继承事实；Compact/审计 Append → compaction.apply（与已知 inference.finish 原子提交）；Archive → 独占下 state.update.archived_at，已有 writer 自行 Append。状态/配置管理方及 runtime 适配层用类型化 Append，不增 Store 动词；设置文件归属不变。读/Open/Close 不写对话事件；显式恢复写控制元数据，删除写生命周期元数据。

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

| 子 agent 边界 | 契约 |
| --- | --- |
| 派发 | 预留子 ID；派发前创建/认领并验证父级/任务/子会话所有权；每次继续执行用新 start。 |
| 接纳 | 父只存协调/接纳内容，不存子事件流。已知结果 + finish 合批，不虚构空输出。独立内容/资源、新接收方原生 ID、对齐 source_message_ids，保留提供方字段/工具身份。 |
| 重试/投递 | 可信层跨重试/重启保持接纳事件/消息 ID；内容/来源/接收方变化冲突，有意投递用新 ID。接纳后确认，仅推理输入 ID 证明消费。传输层/群组收件箱负责路由/重试；无会话队列、跨会话事务或恰好一次执行。 |
| Runtime | 延迟结果归原 writer，不归选中 UI/分叉。源删除仅影响溯源，不影响留存。外部名单/收件箱归属不变；恢复协调/认领提示，切换释放成员，分叉清空提示。回放不重建进程/回调/子所有权，活跃继续执行重新验证。 |

```text
fixed nonempty source prefix              staged fork (protect source until publish)
seq 1: create E1 -----------------------> create F1 (source initial config)
seq 2..N: E2..EN -----------------------> F2..FN (fresh global event IDs; same seq)
local refs: E* -------------------------> F* (envelope AND payload)
foreign refs / native message IDs / seq anchors ----> unchanged
assets --------------------------------> independent copies + rewritten refs
inherited replay --> destination adjustments + clear group_hint --> publish whole log
```
重映射**所有**本地事件引用：source_id、operation_ref、result_event_id、input/effect；必需映射缺失失败。保留历史补丁，只追加必要子本地调整。历史委派不改变真实子父级或转移投递映射/执行/权限/群组权威；从头回放等于 Fork 结果，源删除后内容仍完整，未完成尝试不重启。

**压缩：** 有效上下文 → 原生摘要 + 有序保留消息；摘要索引到 History，不另写消息事件。保留历史/config/state/资源（存储无损 ≠ 摘要无损）。先串行捕获输入/提交 start，生成在提交锁外；共用 Append/Compact，验证批前尾、摘要身份、同会话已有唯一引用、有效顺序及工具调用/结果一致性，可保留仍有效的旧摘要。

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

| 清理边界（拟议，非已实现设置键/参数） | 必需行为 |
| --- | --- |
| 授权 | 自动清理限 OpenStore 捕获 workspace，扩大须显式项目级授权。检出本地策略不删其他 workspace；全局来源、更宽 List、IDs 均不扩大权限。复用现有配置；缺失/不可信/歧义策略阻止破坏，变更不静默缩短期限。 |
| 范围/预览 | Store 项目 ∩ 获准 workspace ∩ 可选 IDs，不沿父子/分叉扩展。DryRun 不写/修复/认领租约，报告恢复阻塞；Limit 有限默认/上限。逐 ID 返回动作/原因、可恢复性/期限、验证字节或未知、截断及取消/失败部分结果。 |
| 确认/竞态 | 只提交选定返回 IDs；预览不预留/授权。writer 互斥下复查策略、前缀/控制/生命周期修订、墓碑/所有者；变化则跳过/报告，不替换/扩大。定时清理另行授权；重试代次内部管理，不引入客户端令牌/回执/操作 ID。 |
| 保护 | 固定、空闲 writer、后台工作、待投递/审批、未完成/未知执行或活动、损坏/未知模式。协调 fork/import/backup/投递隔离，否则跳过；崩溃工作先核实。看活跃依赖而非祖先；Delete/Restore 同样执行适用保护，无级联/子树事务。 |
| 回收站转换 | 稳定项目分区，与日志/资源同文件系统，不在 workspace/cwd。元数据含身份/清单/验证前缀/授权/期限/重启进度；移动前隔离发现/Open/旧句柄。多文件移动不原子；不完整转换保护至恢复/安全回滚，不暴露部分历史。 |
| Restore | 验证内容/引用/原关联；缺失/损坏/purge 已开始失败。冲突不覆盖/合并/换 ID；完整发布原身份/历史/归档/固定，不授予 runtime 权威；重试区分已恢复代次与后来删除。 |
| 永久删除 | 仅 Clean 清除到期回收站：持久不可用标记 → 可重启回收验证所属文件 → 完成前保留生命周期依据；不复活。 |
| GC/缓存驱逐 | 协调读写；扫描全部保留历史（压缩前/归档/固定/回收站），保护暂存/发布/备份。仅已验证临时/孤儿文件过宽限（如 24h），unlink 前复查引用。不全扫描/未知归属或格式/歧义引用则跳过；日志、引用资源、活跃租约非垃圾。 |
| 调度/压力 | 交互就绪后/空闲有界运行；无 CLI 则无守护进程。到期仅获下次安全运行资格；压力只提示/预览/导出/明确选择，不缩短期限或绕过保护。 |

## 落地与验证

```text
legacy schema 4 + SDK codec/truncate --> existing readers (originals preserved)
                  |
                  v
SDK batches + fenced ownership + durable admission + recovery/cache fault tests
                  |
validated adapters --> old/new parity gate --> approved exclusive retryable conversion
                  |   context/history/native IDs/assets/inspector incl. replacements
                  `-- unsupported/missing facts --> BLOCK; no fabricated/discarded facts
conversion --> new format (no dual writes / silent partition merge)
rollback after new writes --> preserve/export new history FIRST
```
移除 SQLite 实验/依赖，保留文件元数据、sync、读锁优化/测试及原生行为。测试 TUI/无界面/ACP、hooks/inspector、三条读路径；注入 write/sync/rename/unlink、部分批次/移动、不明确提交/备份故障。先测摘要/锁/回放分配再做检查点；后端测试不证明目标契约。
验证：身份/worktree/配置隔离/信任、18 类事件无回放副作用、原生/提供方往返、重试与有意重复、委派/分叉独立性/延迟回调、连续/审计/过期压缩、固定前缀浏览/当前策略恢复、保留默认/固定/未知保护、预览竞态、中断 trash/restore/purge、GC/备份。
未决：位置迁移/协调 UX、注册格式、最终摘要/载荷、SDK 批次集成/转换记账。已定：Git common-directory 身份、非 Git 精确 cwd、检出本地配置；SQLite（含索引）须另决策/新证据。

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
