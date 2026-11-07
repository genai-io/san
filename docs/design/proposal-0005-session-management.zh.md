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
                                            ├─ session.create
                                            ├─ message.append
                                            ├─ compaction.apply
                                            ├─ config.update
                                            ├─ state.update
                                            ├─ annotation.add
                                            ├─ inference.start
                                            ├─ inference.finish
                                            ├─ tool.start
                                            ├─ tool.finish
                                            ├─ delegation.start
                                            ├─ delegation.finish
                                            ├─ permission.request
                                            ├─ permission.decide
                                            ├─ hook.finish
                                            ├─ instruction.update
                                            ├─ toolset.update
                                            └─ skill.update

Events ── 投影器 ──┬─ History：展示历史，包含压缩前消息
                  ├─ Snapshot：活跃上下文及持久状态，用于 resume
                  └─ Summary：可重建的会话列表摘要
```

每行事件只属于一个 Session，由 `type` 决定类型化的 `payload`；上面的分类不是独立的表。
消息是事件内容的一种，不代表全部事件。`message.append` 保存追加的原生对话内容，
`compaction.apply` 保存其原生摘要；
`tool.finish` 补充执行/展示元数据，不重复保存工具结果正文。
父子关系和分叉关系连接的是 Session，不共享可变事件行。
History 和 Snapshot 是同一份事件的不同视图，不是各自保存一套数据。
这里的 Event 指持久化事实，并非每个流式 token 或 runtime 临时通知。
`Event` 是拟议存储封装；当前代码仍使用 `transcript.Record`，需要明确适配，不能直接改名。
类型平铺，使用动词原形，统一为 `<object>.<action>`。事件 API、提交边界和回放契约
使其表示已记录的事实，而非依赖语法时态：compaction.apply 绝不会命令回放过程生成摘要。
Apply 表示已接受的状态转换；finish 表示终止结果，不一定成功。
Session 归属已由 `session_id` 表达。这是目标命名，不是当前编码格式；导入时必须明确映射旧类型并保留语义。
归档属于状态；当前执行 cwd 属于会话状态，不改变归属工作区身份。

### 事件应用：边界与动作

归属路径是 projects → workspaces → sessions → session_events，
分别通过 project_id、workspace_id、session_id 关联。每个事件属于一个 Session，
不直接属于 Project。用户/项目配置仍由原有管理方负责；会话事件只记录会话覆盖项和执行事实。
没有明确载荷、归属和消费方的事件，不纳入设计。

```text
实时命令 → 校验事件并准备确定性的投影
         → 事务：追加事件并更新派生摘要
         → 提交 → 发布投影 → 实时协调层按需同步运行环境

Resume → 读取已提交事件 → 相同的投影规则 → 恢复最终状态
       → 执行前解析当前配置并检查信任
```

下表的 trigger 指应用已提交事件后的确定性变化，不是再次派发业务命令。
回放不调用模型、不执行工具/Hook、不弹出授权请求，也不再次追加同一事件。
配置和 cwd 变化由实时协调层在提交后处理；resume 只协调最终状态，
不逐次执行历史中的中间变化。

### 事件信息契约

每个事件使用下文表定义的封装：身份/顺序、类型/版本/时间、可选的关联、因果、投递和结果列，
以及类型化载荷。ID 在提交请求前分配；seq 在事务提交时分配，绝不从时间戳推断。
载荷不重复封装字段。下文后缀 `?` 表示可选，其他载荷字段均必填；
各类型的封装字段要求见关联契约。原生消息、工具定义和待办保留 SDK/产品模式，不简化为文本。
未知的可选值应省略，不填入虚构默认值。具体 Go 类型须通过 SDK 对齐检查。

| 事件 | 类型化载荷字段 | 应用后的动作 | 不做什么 |
| --- | --- | --- | --- |
| `session.create` | workspace_id, initial_cwd, agent_id, sidechain；parent_session_id?, fork_source_id?, fork_source_seq?, initial_config? | 初始化身份和投影；关联分叉来源 | 不启动 agent、不创建项目、不再次复制文件 |
| `message.append` | messages：非空、有序的原生消息列表；source_message_ids?（与接收的原生消息逐项对齐的源 ID） | 建立消息索引，追加历史及有效消息序列，派生预览/计数 | 不调用模型，不执行消息中的工具调用 |
| `compaction.apply` | source_seq, summary：原生消息, retained_message_ids：有序列表 | 索引摘要，有效序列切换为「摘要 + 保留消息」 | 不删除历史/资源，不修改 config/state |
| `config.update` | patch: ConfigPatch | 更新会话覆盖项，使下次有效配置解析失效重算 | 不修改用户/项目文件、历史或进行中的请求 |
| `state.update` | patch: StatePatch | 更新指定状态及摘要；cwd 改变时标记环境待刷新 | 不替换消息、不改变归属工作区、不授予权限 |
| `inference.start` | turn_id, input_message_ids, request_config, instruction_digest, toolset_digest | 登记未结束的尝试及实际使用的非秘密请求来源信息 | 不请求模型 |
| `inference.finish` | stop_reason?, usage?, duration_ms?, error?, reason? | 结束尝试，更新审计/用量；输出保留在消息事件中 | 不复制输出、不自动重试 |
| `tool.start` | call_id, input_ref | 登记未结束的执行；input_ref 标识持久化的调用及参数 | 不授权、不执行工具 |
| `tool.finish` | call_id；duration_ms?, error?, reason?, details? | 结束尝试，关联类型化执行/展示详情；结果正文保留在消息/资源中 | 不复制结果正文、不重复副作用 |
| `delegation.start` | child_session_id, input_ref | 登记一次已接纳的子会话执行/继续执行及其输入 | 回放时不启动子会话，不复制其事件流 |
| `delegation.finish` | error?, reason? | 结束委派尝试；存在已接纳结果时通过因果引用关联 | 不终结子会话，不重复结果正文 |
| `permission.request` | operation_ref, scope | 增加关联受保护操作的待决审计项 | 回放时不弹出请求 |
| `permission.decide` | decision, scope；reason? | 完成对应请求的审计项 | 不把历史授权恢复为当前授权 |
| `hook.finish` | hook_name, trigger；duration_ms?, reason?, effect_refs? | 记录已知 Hook 结果及单独持久化的效果引用 | 不执行 Hook，不再次应用其效果 |
| `instruction.update` | upsert：具名指令片段, remove：片段名称；source? | 更新请求摘要所引用的历史指令目录 | 不覆盖当前指令文件，不注入消息 |
| `toolset.update` | upsert：原生工具定义, remove：工具名称；source? | 更新请求摘要所引用的历史工具目录 | 不安装工具、不启用执行 |
| `skill.update` | changes：具名、类型化的技能启用状态；source? | 更新已观察到的启用历史，供检查 | 不加载技能、不编辑偏好 |
| `annotation.add` | text；anchor?（消息 ID 或事件序号） | 在锚点处增加仅供展示的持久注释；无锚点则属于会话级 | 不进入模型上下文、不改变状态、不触发执行 |

生产方明确划分：Store.Create/Fork 负责身份；已接纳消息的适配层负责消息；
Session.Compact 负责压缩；设置/会话管理方负责补丁；模型/工具执行适配层负责尝试记录；
审批和 Hook 管理方负责各自审计；委派协调方负责子会话执行事实；请求准备阶段负责指令/工具集合变更；
技能启用流程负责已观察到的技能变更；应用功能负责注释。渲染器只消费数据，绝不是持久化来源。

ConfigPatch 允许 provider、model、generation_options、mode 和 autopilot_policy。
生成参数和 autopilot 策略使用带版本、白名单内的产品字段，不接受任意提供方 JSON。
StatePatch 允许 title、archived_at、todos、autopilot_progress、execution_cwd 和 group_hint。
嵌套值均有类型；group_hint 仅保存重新认领所需的群组/成员身份，不是名单或权限的权威来源。
补丁字段区分未提供（不变）、设置和清空；清空按类型契约移除覆盖或重置字段。
false/零/空值是显式值，不隐含清空。拒绝未知或跨归属字段。
未变化的补丁和定义增量不产生操作。

### 关联与来源

通用查询/校验字段作为列，不埋在载荷中：

| 封装字段 | 含义与必需用法 |
| --- | --- |
| `correlation_id` | 会话内单次操作身份：inference/tool/delegation start/finish、permission request/decide、hook finish 和 compaction apply 必填。配对事件共用；执行重试使用新 ID，持久化重试保留原 ID。无关事件为空。 |
| `cause_session_id`, `cause_event_id` | 一个直接因果事件，两者同时存在或同时缺省。生成的消息/摘要指向生产方 start；接收消息指向源内容，委派输入则指向 delegation.start。沿引用追溯，不复制整条路径。 |
| `delivery_id` | 跨会话消息接纳时必填；标识一次已接纳批次，重投时保持稳定。其他事件为空。 |
| `outcome` | 仅 inference/tool/delegation/hook finish 必填：success/failure/cancelled。权限决定仍在其类型化载荷中。 |

Correlation 不是全局追踪 ID。它替代载荷中事件自身操作的 attempt/request/invocation/operation ID；
原生 call_id 和 turn_id 仍是类型化载荷字段。压缩有自身 correlation，不同于其 cause 指向的推理。
因果列替代 producer_ref，而非复制它。事件可以没有 cause，例如直接用户输入；不得虚构。
类型、本地消息角色和因果链共同标识来源，无需冗余的来源类别列。
事件专属的输入/内容引用仍在类型化载荷中；单个直接原因不等于完整依赖图。

### 校验与关联

- 创建必须是 seq 1 的首个事件，身份必须尚不存在；fork_source_id 和 fork_source_seq
  要么都省略，要么指向已有的非空源前缀。初始默认值是类型化配置，不含秘密。
  Fork 按下文规则构造唯一的子会话创建事件，不再追加第二次创建。
- 原生消息 ID 非空且唯一；同一 ID 对应不同内容即冲突。压缩按下文规则验证源版本和引用。
- 推理/工具生成的消息批次及摘要必须通过因果列指向生产方 start。每批只有一个 cause；混合来源须拆批。
  按该次执行校验原生工具调用 ID。因果与内容一同不可变，绝不从时间戳或相邻位置推断。
  旧记录的未知来源经兼容映射后仍明确为未知。
- Start/finish 按同一会话、同一执行类别中的 correlation_id 匹配；工具调用 ID 也必须匹配。
  每次操作只允许一个 start、至多一个终止结果。不同操作复用 correlation 即冲突。
  失败需要有界的结构化错误，取消需要原因。未知结果不得伪造 finish。
  重复提交相同结果具有幂等性；结果冲突则失败。
- 权限决定必须匹配请求 correlation ID；decision 为 allow/deny/cancelled，作用域不能超出请求。
  Hook 派发确认不等于已知完成结果。Hook 效果只能通过其自身事件/管理方改变状态，不能通过回放应用。
- 本地 cause 及必需的消息/事件/资源引用必须解析到已提交前缀，或同一事务中已验证的更早条目。
  外部因果来源及父会话/分叉身份是明确允许的跨会话例外。可信适配层在接纳时校验外部 cause；
  导入时不可用的来源需要显式兼容映射，不得伪造引用。回放在本地解析内容，不要求外部 cause 仍存在。
  input_ref 标识不可变的调用名称/参数，指向原生调用或已持久化的调用资源。
  必需内容缺失必须失败；外部来源删除可能中断追溯，但绝不删除本地已接纳内容。
- 指令/工具集合更新使用唯一名称、互不相交的 upsert/remove 集合及类型化内容；
  空的初始目录是已知值，不代表历史缺失。请求摘要必须能解析到已记录的目录版本。
  技能变更的名称唯一、状态经过校验。不要在每次调用中复制未变定义。
- 未知类型/版本或无效字段在提交前拒绝。无实际变化的命令不产生事件。
  缺少必填字段的旧记录需要显式兼容映射，保留当时已知信息；不得为适配模式而伪造执行事实。

### 委派与跨会话输入

子 agent 拥有独立会话/事件流。父子关系标识会话；一次 delegation correlation 标识一次执行或继续执行，
而非子会话的整个生命周期。父会话只记录协调事实及实际接纳的内容：

```text
父会话：tool.start → delegation.start
                         │ cause
                         ▼
子会话：session.create / 已接纳输入 → inference/tool/message 事件
                                                       │ 结果内容
                                                       ▼
父会话：message.append ──────────────────────→ delegation.finish
        cause = 子会话内容                     cause = 已接纳结果
        delivery_id = 接收凭据                  correlation = delegation.start
```

提交 delegation.start 前先分配目标子会话 ID，派发前创建/认领子会话；创建失败可能留下无对应会话的预留 ID。
执行前校验当前父级/任务/子会话所有权。新子会话的创建或已有子会话接纳的继续执行输入，
都以父会话 delegation start 为因果引用。工具派发确认不等于委派完成。每次继续执行使用新的 delegation correlation。
已知完成、失败或取消会结束该次尝试；崩溃后在核实前保持未知，绝不能因此在回放时启动工作。
结果消息与 delegation.finish 同时已知时一并提交；finish 引用本地已接纳结果，不再复制正文。
没有结果正文时，无需为 finish 虚构消息。

跨会话内容使用 message.append，不另设接收消息类型。接收方以新的、稳定的接收方本地消息 ID，
保存已接纳内容及独立保留的资源。对于原生消息输入，source_message_ids 将每条本地消息映射到
因果源事件中的原始 ID；保留提供方字段和工具调用身份，不再复制一份源正文。
上下文顺序、推理输入和压缩使用本地 ID。重投复用已接纳 ID；真正的新投递即使源内容相同也分配新 ID。
这是消息接纳，不是保留源本地消息身份的无损导入。

Cause 指向上游内容事件；例外是委派输入，指向 delegation.start 并解析其不可变 input_ref。
一次投递是一批同源内容；同一源事件的多次投递使用不同 delivery ID。可信传输层分配抗碰撞的 delivery ID，
重试时保留，并验证来源/会话所有权；消息文本不能声明权限。同一接收方/delivery 且内容和来源相同，
返回已有接纳结果；正文或 cause 改变即冲突，即使使用新的事件 ID。原子接纳后才能确认。
接纳不表示模型已消费；实际使用内容由推理输入 ID 决定。本地转换后的内容引用本地生产方，
再通过其输入引用追溯已接纳来源；不能声称是原样副本。

传输层/群组收件箱负责路由、重试和投递保证。会话历史记录接纳事实，不另建队列，也不记录每次收发通知。
不隐含跨会话事务或恰好一次执行。延迟结果路由至原会话 writer，而非 UI 当前选择或分叉会话。
来源删除后仍保留其身份用于追溯，但追溯可能到此为止。不持久化心跳、进度或转发的子会话事件流；
回放绝不重发消息、授予权限或取得子会话所有权。

### 合并规则

创建与分叉共用一次创建转换：分叉来源是 session.create 的可选数据，不重复产生创建事件。
成功、失败、取消共用一次 finish 转换，结果字段必填；重试保留独立尝试 ID，
冲突的终止结果不能覆盖已知结果。Start 表示派发前已接纳尝试，不证明远端已收到请求。
只有 start、没有 finish 结果的尝试保持未知，不能伪造失败。
事件保持平铺，不引入通用 type/phase/action 封装。

保留配置意图与状态事实、工具执行与工具集合定义、指令定义与展示注释的区别。
技能历史记录供检查的实际启用状态，不是另一份可编辑偏好。
压缩仍只有一个 apply 事件，不增加 start/finish 或替换事件；其摘要生成请求可以有推理审计记录。

### Config 与 state 的边界

两者保留为独立的类型化事件：后续运行意图与当前会话事实。会话配置覆盖不复制 User/Project 设置。
Annotation 不属于这两者，它是持久展示说明，例如重新加入群组失败的说明，
不带模型角色或状态补丁，并遵循下文的记录准入规则。

| 归属 | 允许的字段范围 |
| --- | --- |
| `config.update` | 模型/提供方选择、生成参数、操作模式、autopilot 策略/限制 |
| `state.update` | 标题、归档标记、持久待办/进展、当前执行 cwd、群组恢复提示 |
| 其他事件/管理方 | 消息与压缩、每次请求实际设置/用量、活跃进程、授权及真实群组成员关系 |

Config 表达后续运行意图，state 表达会话当前事实，每个字段只有一个归属。
Autopilot 策略和进展必须分开，不能把整个不透明对象同时放入两者。
补丁校验遵循上述类型契约。回放只恢复意图/事实，不恢复活跃授权。
现有混合 StatePatch 必须逐字段映射；最近提示词预览派生自消息，不能再独立写入 state。
不引入包揽一切的 context/settings 事件。

### 记录契约：事实完整，重复最少

只有新增持久对话事实、已接受的配置/状态变更、必要的执行/授权审计事实，
或不可重建的历史说明，才允许记录为事件；生产方、消费方及回放含义必须明确。
运行时可观察并不足以入库。开始、失败、取消和权限拒绝即使不改变有效上下文，
仍能解释尝试过的工作及恢复时的不确定性，因此有记录意义。这不是通用调试日志。

推理/工具执行前先持久化开始事件。执行边界不同于持久化的助手工具调用消息：
调用消息可能在获准执行前就已存在。必要的开始事件无法提交时，不执行操作。
适配层必须建立这些目标保证，不能假设当前回调已经具备。
完整输出和终止结果同时已知时，在一个 Append 批次中提交输出及 finish。
较早的完整消息可能已经提交；因果引用不意味着尝试已结束。崩溃后保留这些输出，
在核实前让尝试保持未知；回放绝不再次派发。尝试的输出从不可变因果引用派生，
不在 finish 事件中另存输出列表。带审计的压缩遵循下文顺序。

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
| `CreateOptions` | 工作区 ID、初始 cwd、可选 ID/父会话及不含秘密的初始设置；委派创建必须成对提供 `cause_session_id` / `cause_event_id`，由 Create 校验并持久化 |
| `Create` / `Open` | 返回持有写入所有权的句柄；Open 先认领，再加载一致的投影 |
| `Get` / `List` | 读取摘要而不认领写入所有权，包括由其他进程持有的会话 |
| `SessionQuery` | 项目/工作区/父会话/群组/sidechain/归档筛选、有界条数上限、不透明游标 |
| `Events` | 有序原始事件；游标绑定筛选条件和固定的 `ThroughSeq`，避免追加影响分页 |
| `History` | 该固定前缀的展示投影，包含压缩前历史；附件以引用提供 |
| `Snapshot` | 不可变的已提交上下文、应用状态和恢复位置；不含 UI 行或活跃资源 |
| `ForkOptions` | 可选目标工作区和源序号；否则捕获最新已提交前缀 |
| `CompactionChange` | `source_seq`（预期当前前缀）、压缩操作的稳定 `correlation_id`、含稳定 ID/提供方内容的完整原生 `summary`、有序 `retained_message_ids`、生成摘要的因果身份 |
| `Append` / `Compact` | 共用原子写入实现及压缩校验；Append 还可将压缩及其推理 finish 合批，Compact 则封装单次转换 |
| `Archive` / `Delete` | 归档追加状态；硬删除需要所有权，并拒绝存在活跃写入者的会话 |
| `Close` | 等待进行中的写入，且仅释放一次所有权 |

### 会话生命周期与执行生命周期

Session 是可恢复的对话，不是一次程序启动或 agent 运行，因此不强制配对创建和结束事件。

| 操作 | 持久化影响 | 能否 resume？ |
| --- | --- | --- |
| 创建 / 分叉 | 通过 session.create（分叉包含源身份/序号） 建立身份及来源 | 能 |
| Open / Close | 取得/释放 writer；Close 仅关闭当前句柄，不终结对话 | 能 |
| 执行完成 / 取消 | 记录已知推理/工具结果，消息保留 | 能 |
| 崩溃 | 无法保证写入结束事件；未完成尝试在确认前保持未知 | 能，需恢复检查 |
| 归档 / 取消归档 | 状态补丁改变可见性，不改变可写性或历史 | 能 |
| 删除 | 显式移除会话/事件并回收附件，不是结束事件 | 不能 |

不能从最后一条事件推断当前是否正在执行。Runtime 报告实时执行状态，lease 仅决定所有权，空闲时也可持有。
初期不定义 session.end。
未来若增加终结事件，必须明确禁止后续写入及如何重新打开，不能仅表示程序退出。
执行遥测不能永久终结会话。

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
| 操作/因果/投递身份及终止结果 | `session_events` 的一等列；单一权威来源，不在载荷中镜像 |
| 原生消息、压缩内容、会话覆盖项、待办、历史注释和审计详情 | 类型化的 `session_events.payload` |
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
| `correlation_id` | TEXT ? | 会话内操作身份，由其生命周期事件共用 |
| `cause_session_id` | TEXT ? | 直接因果事件所在会话；历史身份，不表示所有权 |
| `cause_event_id` | TEXT ? | cause_session_id 内的事件 ID；与该列成对 |
| `delivery_id` | TEXT ? | 一次已接纳跨会话消息批次的稳定传输身份 |
| `outcome` | TEXT ? CHECK success/failure/cancelled | 已知执行终止结果；其他类型为空 |
| `payload` | TEXT, validated JSON | 仅含事件专属内容，不重复封装字段 |

主键 `(session_id, seq)`；唯一约束 `(session_id, id)`、`(session_id, delivery_id)` 和
`(session_id, type, correlation_id)`（空值不冲突）。最后一项防止重复 start/终止记录；
应用校验还禁止不同操作类别复用 correlation。索引 `(session_id, correlation_id, seq)` 用于操作查询，
`(cause_session_id, cause_event_id)` 用于反向追溯。两个 cause 列必须同时为空或同时非空。
提交前按类型校验必填/禁止的列。外部 cause 身份刻意不设删除外键：
历史来源绝不级联删除、清空或阻止删除。本地引用完整性由追加/导入校验保证，而非回放时获取外部数据。

将共用身份、去重、因果查询和终止状态字段提升为列；原生内容、作用域、输入引用、补丁和诊断仍在类型化载荷中。
不要为每个可选字段推测性加列。项目/工作区身份通过关联会话获取，不复制到每个事件。
原生消息 ID 不同于事件 ID；原生角色不是另一套封装 actor/source 字段。
编解码器必须保留签名、加密推理、图像/内容顺序、工具 ID
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

一次成功压缩只提交一个 `compaction.apply` 事件，不再拆成替换和完成事件：

封装中的 correlation 标识压缩操作，用于幂等重试；因果列标识生成摘要的推理 start。两者均不在载荷中重复。

| payload 字段 | 含义 |
| --- | --- |
| `source_seq` | 生成摘要前捕获的已提交会话版本；若有审计，则在其推理 start 提交后捕获 |
| `summary` | 完整原生摘要消息，包括稳定消息 ID、角色、内容及提供方字段 |
| `retained_message_ids` | 按原有效序列顺序保留的消息 ID，不复制正文 |

投影结果为「摘要 + 引用的保留消息」。引用必须指向同一会话源版本中已有的不可变消息，
包括仍有效的先前压缩摘要；新摘要进入消息索引和 History，无需另写一条消息事件。
原文、工具结果、图像、签名、内容顺序和消息 ID 继续保留，工具结果不能成为悬空引用。

带审计的摘要生成须串行完成输入捕获及 inference.start 持久化，派发前将所得已提交前缀作为 source_seq。
成功且获接受时，一个 Append 事务校验该前缀，并依次追加 compaction.apply 和 inference.finish。
比较的是批次提交前的尾序号，不是批次最终序号。审计 start 已在捕获的前缀中，finish 不会使自身 apply 失效。
此保守规则下，期间任何其他写入都会使结果过期；初期无需独立的上下文版本字段。

提交前校验源版本未变、摘要身份无冲突、引用存在且唯一、顺序合法，
并验证保留的工具调用/结果关系符合原生消息约束。缺失、跨会话或越过源版本的引用必须拒绝。
事件和派生摘要/序号在同一事务提交，提交后才发布内存投影。
同一操作及内容的重试不新增事件；同一操作 ID 对应不同内容报冲突。
先检查操作是否已提交，再检查源版本，避免把成功操作的重试误判为过期。
拒绝已退役会话/runtime 的延迟结果。

失败、取消或无需压缩时不提交压缩事件，有效上下文不变。推理 finish 描述生成结果，而非是否接受：
摘要因过期被拒绝，推理结果仍可成功。必要时只记录已知结果而不 apply，不为审计保存未使用的摘要正文。
apply/finish 事务失败时，两者都不发布；提交结果不明确时，先核实，再单独记录已知推理结果。
提交后崩溃，resume 必须恢复已提交结果，即使 UI 尚未显示。
提交结果不明确时，先确认结果，再重试或接受新写入。
自动、手动、runtime 未启动及连续压缩都使用同一规则。
Fork 遵循下文构造规则，保留的 ID 在本地解析，各会话独立压缩。压缩不清理旧附件。
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
- 引用资源前先原子发布资源。清理排除进行中的操作，并可重试。删除先提交，再回收资源。
- 将一致的 SQLite 快照与引用资源一同备份。WAL 写入期间只复制主数据库文件并不足够。

### Fork 构造

Fork 保护固定、非空的已提交源前缀免遭删除，直至子会话发布。
用唯一的子会话创建事件原位替换 seq 1 的源创建事件，不在前面插入。
子会话身份、归属工作区选项及分叉来源使用新值；保留源初始配置，作为继承回放的起点。
后续事件按原序号位置复制，保留会话内 message/event/correlation/delivery ID。
仅当因果对的会话是分叉来源、且事件属于复制前缀时，才重新绑定到子会话，包括指向被替换创建事件的引用。
外部来源保持不变。本地事件序号锚点和历史压缩 source_seq 保持原位置；fork_source_seq 表示来源前缀。
发布前复制资源并改写本地内容引用。复制的委派目标是历史身份：不得改变真实子会话的父级、
继承其执行所有权或重定向结果。

投影继承前缀后，追加子会话本地状态补丁，应用显式目标调整，并在 group_hint 已设置时清空它。
不得过滤历史补丁，也不能依赖创建时清空。这些补丁必须实际改变值，无需空的清理事件。
创建、继承事件、最终补丁和派生摘要原子发布。复制是经校验的导入，不是重新派发命令：
历史尝试仍是历史，未完成尝试绝不重启。子会话从头回放必须等于 Fork 结果，
不含继承的群组提示，且不依赖来源会话即可解析所有内容引用，即使来源已删除。

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
| 群组切换/重新认领被拒；后台延迟完成 | 不恢复失效成员关系，不错误路由回调 |
| 委派开始、继续执行、崩溃和分叉 | 每次执行一个 correlation；派发确认不等于 finish；未完成尝试保持未知；复制历史不授予子会话所有权 |
| 跨会话投递、重投和来源删除 | 原子本地接纳后才确认；同一投递/内容具有幂等性，内容/cause 改变即冲突；来源删除后已接纳内容仍保留；回放绝不重发 |
| 子会话历史和分叉；父/来源会话删除 | 独立身份/资源；从头回放等于 Fork 结果，保留本地序号引用并清空继承的群组提示；不暗示活跃任务 |
| 重复/失败/带审计的压缩、重试和无效引用 | 一次原子转换含一个 apply 及其待提交的推理 finish；自身审计不导致过期，中途写入则拒绝 apply；上下文/历史等价，配置/状态/资源不变 |
| 配置变更和权限撤销 | 正确的作用域写入，恢复时采用当前安全策略 |
| Config/state 补丁及旧混合字段 | 每字段一个类型化归属；拒绝未知键；预览派生而非重复存储 |
| 回放每种事件 | 得到相同投影，不调用模型/工具/Hook，不弹授权请求，不产生新事件 |
| 关闭/重开、完成/取消、归档及崩溃 | 保留可恢复身份，不伪造终结事件，未知尝试保持未知 |
| 持久注释与派生 UI 文案 | 保留有意义且带锚点的说明，不注入模型，不重复写展示事件 |
| 展示历史、图像和并行工具 | 完整浏览，不扩大恢复后的模型上下文 |
| 崩溃、重复写入、清理和错误检查点 | 一致的记录/摘要；可恢复缓存；显式失败 |
| 调用中断、重试和内容相同的重复输入 | 输出标明生产方；已提交输出没有 finish 时仍可恢复，但结果未知；已知结果结束对应尝试；去重不丢失独立动作 |
| 事件准入、流式/进度通知和未变状态 | 保留新增持久事实及必要的失败/拒绝审计；无逐 token 事件、空补丁、重复快照或可派生注释 |
| 导入、重复导入、导出和回滚 | 保留 ID/内容，只有一个可写权威来源 |

以现有 session/transcript/app 测试为基线。优化前检查选择器响应速度和回放成本。
文档不声称 SQLite 测试已通过。

实现前需确定位置迁移/worktree 关联的交互、最终摘要字段、SDK 事务集成及迁移记账。
若事务/查询收益不足以证明迁移合理，JSONL 仍是有效替代方案。
基于文件的 SQLite 索引仍需处理跨存储一致性；按项目分库可隔离故障，但会增加关联和全局查询的复杂度。
避免推测性模式设计。
