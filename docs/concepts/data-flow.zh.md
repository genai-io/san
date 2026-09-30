# 数据流转：输入 → Agent → 渲染

> English version: [`data-flow.md`](data-flow.md)
>
> 姊妹篇：[`rendering.zh.md`](rendering.zh.md) ——
> 真正的渲染输出是怎么组装的（View() 布局、Markdown pipeline、工具块）。

一次按键（或一次 cron 触发、一条后台通知）如何穿过 TUI，最终变成
slash 命令的结果或 agent 的回复呈现在终端里。

## 角色

TUI 是一个 [Bubble Tea](https://github.com/charmbracelet/bubbletea) 的
MVU 循环。三个 Bubble Tea 原语驱动一切：

- **`tea.Msg`** —— 进入 model 的事件（按键、窗口 resize、spinner tick、
  自定义的进程内消息等）。
- **`Update(msg)`** —— 纯函数；mutate model，返回一个 `tea.Cmd`。
- **`tea.Cmd`** —— 框架会执行它的函数，其返回值会**作为新的 `tea.Msg`
  再投回 Update**。这就是异步活儿回到 model 的机制。看到一个函数
  "return 一个 cmd"，就意味着 Bubble Tea 会跑这个 cmd、把它的输出包成
  `tea.Msg`、再喂给 `Update`。

约定：内部不少 handler 返回 `(tea.Cmd, bool)`。bool 的语义是**"这事件
我接住了吗"**——`true` 中止链路，`false` 让调用方继续往下试。
`(nil, false)` 就是常见的"不是我的活"返回。

输入源全都落地为 `tea.Msg`。**`SubmitToAgent`** 是通往运行中 agent 的
唯一出口。渲染走两条路：`tea.Println`（终端 scrollback）+ `View()`
（底部 UI 条）。

```
   ┌──────────────────────────────────────────────────────────────┐
   │  Inputs                                                      │
   │                                                              │
   │   keyboard      slash command     cron       async hook      │
   │     │                │             │             │           │
   │     ▼                ▼             ▼             ▼           │
   │  handleSubmit  → SlashController  inject*    inject*         │
   │     │                │             │             │           │
   │     └────────────────┼─────────────┴─────────────┘           │
   │                      ▼                                       │
   │               SubmitToAgent(msg)                             │
   │                      │                                       │
   │                      ▼ agent.Send (Append 进队列)            │
   └──────────────────────┼───────────────────────────────────────┘
                          │
   ┌──────────────────────┼───────────────────────────────────────┐
   │  Agent loop          ▼                                       │
   │           ┌─────────────────────┐                            │
   │           │ Queue → LLM → Tool  │   ← 跑在 goroutine 里      │
   │           │     ↘    ↙          │                            │
   │           │     Outbox          │ → core.Event 流            │
   │           └─────────────────────┘                            │
   └──────────────────────┼───────────────────────────────────────┘
                          │
   ┌──────────────────────┼───────────────────────────────────────┐
   │  Render              ▼                                       │
   │             ContinueOutbox tea.Cmd                           │
   │                      │                                       │
   │                      ▼ tea.Msg                               │
   │                  Update → conv.Update → callbacks            │
   │                      │                                       │
   │                      ▼                                       │
   │             CommitMessages → tea.Println → scrollback        │
   │                      │                                       │
   │                      ▼                                       │
   │                   View() → bottom UI strip                   │
   └──────────────────────────────────────────────────────────────┘
```

## Path A —— 文字输入

用户敲 `hello`，按 **Enter**。

```
tea.KeyMsg('h')                  ── 每按一个字符就来一条
   │
   ▼
Update                            update.go
   │
   ├─ case tea.KeyMsg → routeKeypress
   │     │
   │     ├─ tryActivePopup           — 问题 modal、approval modal、或
   │     │                             slash 命令的 picker（/models、
   │     │                             /tools 等弹出菜单）——敲 'h' 时
   │     │                             没有任何活动
   │     │
   │     ├─ HandleImageSelectKey     — 图片选择模式（关闭中）
   │     ├─ HandleSuggestionKey      — prompt-suggestion 模式（关闭中）
   │     ├─ HandleQueueSelectKey     — 队列导航模式（关闭中）
   │     │
   │     └─ handleTextareaShortcut   — Ctrl 快捷键 / Tab / Enter 等
   │           └─ KeyRunes('h') 没匹配 → (nil, false)
   │
   ├─ routeToSubModel                — 没有 sub-model 认领 KeyRunes
   │
   └─ updateTextarea                  — textarea 吃下这个字符
   ▼
View                              view.go      底部 UI 显示 "h▮"
```

`routeKeypress` 里的分发是一个**优先级栈**：当前弹出的 popup（比如
`/models` 之后的模型选择列表）拥有按键的第一优先权；上层都没接住的，
才轮到 textarea 级别的快捷键；都没匹配，最后 textarea 把字符当文本吃掉。

再敲五个字符之后，textarea 里是 `hello`。用户按 **Enter**：

```
tea.KeyMsg(Enter)
   │
   ▼
routeKeypress → handleTextareaShortcut
   │   "shortcut" = 对 textarea 有特殊含义的键
   │   （Ctrl-C/D/L/E/O/U/V/Y/T、Tab、Shift+Tab、Enter、Esc、↑↓ 翻历史）
   └─ case tea.KeyEnter → m.handleSubmit()       update_submit.go
        │
        ▼
   handleSubmit
        Step 1: 读 textarea  ────► "hello"
        Step 2: 流式回复进行中？───► 否（流式时不接新提交）
        Step 3: → dispatchSubmission("hello")
                  │
                  ▼
   dispatchSubmission
        Step 1: 是 "exit" 字面量？──► 否
        Step 2: prompt hook ───────► 放行
                    │  任何 UserPromptSubmit hook（见 hook 包）能拿到
                    │  这条 prompt，可以拒绝它（例如策略 hook 看到密钥
                    │  就拦截）。"放行" 意味着没有 hook 阻止。
        Step 3: 记录到输入历史（textarea 里的 ↑/↓ 回溯）
        Step 4: 是 slash 命令？───► 否（开头不是 "/"）
        Step 5: 发给 agent
                  ├─ buildUserMessage("hello") → ChatMessage{Role: user}
                  │     解析图片引用（`[image.png]` → 字节）并把内联
                  │     粘贴的图片从文本中分离出来。
                  │
                  ├─ conv.Append(msg)
                  │     追加到 m.conv.Messages。这是 TUI 自己的**显示
                  │     副本**——View() 把它渲染成 scrollback，
                  │     PersistSession 把它写盘。agent **不会**在每次
                  │     发送时读取这个切片；它维护自己独立的内部消息
                  │     历史。两边通过事件保持同步（见 Path D）。
                  │     conv.Append 被 agent 读取仅有一种情况：
                  │     ensureAgentSession 用它给新启动的 agent 喂种子。
                  │
                  ├─ userInput.Reset()
                  │     清空 textarea + 待发送的图片，用户可以开始
                  │     下一条消息。
                  │
                  └─ SubmitToAgent(msg)   ← 与 conv 行共用同一个 ID
                        把 `msg` Append 进 agent 的消息队列（SDK agent
                        那一份）。agent 的 loop 在下一个步骤边界把它加进
                        内部 history（空闲时直接用它开一轮），然后调 LLM。两个调用都需要的
                        原因：
                          conv.Append(msg)  → 让用户**看见**
                          SubmitToAgent     → 让 agent **干活**
                        │
                        ▼
   SubmitToAgent
        ├─ provider 连上了吗？     是
        ├─ ensureAgentSession()    必要时启动 agent goroutine
        ├─ sendToAgent ───────────► core.Agent.Append
        │                           （任意 goroutine，永不阻塞）
        │
        └─ 返回 ContinueOutbox cmd  （见 Path D）
              这个 cmd 被 Bubble Tea 跑起来时，会从 agent 的 Outbox
              channel 读出一条事件、包成 tea.Msg 回投到 Update。
              LLM 开始流式输出后，第一条事件通常毫秒级到达。
```

## Path B —— Slash 命令

用户敲 `/clear`，按 **Enter**。路径在 Step 4 之前和 Path A 完全相同：

```
handleSubmit → dispatchSubmission
   Step 1..3 同 Path A
   Step 4: runSlashCommandIfMatched("/clear")
              │
              ▼
   input.NewSlashCommandController(slashCommandEnv)         slash_command.go
              │
              ▼
   SlashCommandController.HandleSubmit
              │ ParseCommand("/clear") → ("clear", "")
              ▼
   handleClearCommand(c, ctx, "")
        ├─ env.StopAgentSession()         清掉 agent 状态
        ├─ env.PersistSession()           存当前对话
        ├─ env.Conversation.Clear()       擦掉显示
        ├─ env.Input.Reset()
        └─ 返回 (result="conversation cleared", cmd=nil, nil)
              │
              ▼
   c.env.Conversation.AddNotice(result)    显示 "conversation cleared"
   c.env.CommitMessages()                  → tea.Println 写入 scrollback
```

每个 slash 命令的 handler 通过 `env.*` 读 service 实时状态、通过回调
（如 `env.PersistSession`）触发副作用，返回一个简短的 `result` 字符串，
由 controller 包装成 notice 显示。

部分 slash 命令（`/loop`、`/init` 等）会调 `env.SubmitToAgent(msg)`
把内容交给 agent——它们在 SubmitToAgent 这一步重新汇合到 Path A。

## Path C —— 后台触发器

有三个"生产者"在用户不打字的时候独立运行。它们各自把产物放进自己的
队列/channel，然后**回合边界**（agent 一个 turn 结束的时刻）一次性
排空它们。

### 生产者

```
┌─ 生产者 ──────────────┬─ 在哪里运行 ────────┬─ 产物落在 ────────────────────┐
│ Cron 触发             │ trigger.StartCron-  │ m.systemInput.CronQueue       │
│   （ticker 每分钟检查 │ Ticker（后台         │   []string（排队的 prompts）  │
│    持久化的 cron 任务，│  goroutine）         │                               │
│    到点的入队）        │                     │                               │
├───────────────────────┼─────────────────────┼───────────────────────────────┤
│ 异步 hook 后续         │ trigger.StartAsync- │ m.systemInput.AsyncHookQueue  │
│   （hook 脚本的 JSON   │ HookTicker          │   每项带 Notice + Context     │
│    输出里指定了        │                     │   行 + ContinuationPrompt     │
│    `nextPrompt: ...`） │                     │                               │
├───────────────────────┼─────────────────────┼───────────────────────────────┤
│ Subagent 完成          │ task.SetLife-       │ broker.Send 到 "main"；主循环  │
│   （后台 Task，由      │ cycleHandler →      │ 的投递函数把它推到             │
│    Agent 工具拉起）    │ broker.Send         │ m.mainNotices（Go channel）    │
│                       │                     │                               │
└───────────────────────┴─────────────────────┴───────────────────────────────┘
```

`m.mainNotices` 承载发给主循环的通知：subagent 完成、中途消息
（`SendMessage to="main"`）、self-learn 通知。agent 之间的消息走进程级的
[`broker`](../packages/2-feature/broker.md)；主循环注册 `broker.Main`，
把收到的转到这个 channel 上。

### 回合边界排空

活的 agent 这轮 turn 结束时，`OnTurnEnd` 调 `drainTurnQueues` 取出下一
个优先级最高的排队项，当作用户刚刚输入了它一样发出去。

```
OnTurnEnd                                    model_agent_events.go
   └─ drainTurnQueues                        model_turn_queue.go
        优先级从高到低，第一个非空的胜出：
        │
        ├─ 用户输入队列?     ─── 流式期间排过队的（直接发，不走 inject*）
        ├─ cron 队列?         ──► injectCronPrompt(prompt)
        └─ 异步 hook 队列?    ──► injectAsyncHookContinuation(item)
```

通知不在这个列表里：它从不等回合边界（见下）。

### 通知投递

通知（后台任务完成、`SendMessage to="main"`、self-learn）随时会到
`m.mainNotices`，所以借用 agent outbox 的同款套路——一个**阻塞接收的
`tea.Cmd`**，把"chan 上的下一条"转成 `tea.Msg`：

```
Init                                       model.go
   └─ awaitMainNotice(m.mainNotices)         model_turn_queue.go
        └─ 阻塞读 chan，到一条就 yield mainNoticeMsg{notice}

Update                                     update.go
   case mainNoticeMsg:
        └─ onMainNotice(n)                 model_turn_queue.go
              ├─ deliverNotice(n)
              │    ├─ 空闲          → injectAsNewTurn(n)：显示一行 + 开新一轮
              │    └─ 有轮次在跑    → 立刻 sendToAgent(content)，并且
              │         ├─ 末尾空闲（工具在跑）→ 立刻显示那一行
              │         └─ 流式输出占着末尾 → 那一行先存进 m.heldNotices，
              │              在 OnStepEnd / OnTurnEnd 显示
              └─ 重新挂一次 awaitMainNotice，等下一条唤醒
```

通知分两半，约束不同，只有显示那一行需要等屏幕。内容一到就交给 agent：
它的队列会在当前轮次的下一次推理前把内容放进对话，而且一轮不会带着没读的
内容结束——所以不会出现"结果已经显示在屏幕上，模型还在盲跑"。

生产者一侧：subagent（或任何后台 task）完成 → `notifyTaskCompleted` →
`wireTaskLifecycle` 里注册的 lifecycle handler 把完成消息发到 "main" 地址
→ `broker.Register(broker.Main, …)` 的投递函数推入 `m.mainNotices`。后台
task 是一种生产者；subagent 的中途消息（`SendMessage to="main"`）和
self-learn 通知是另外几种——都走同一个 channel。

跟 `conv.DrainAgentOutbox` 读 agent outbox chan 是同一个套路——两个
Go channel、两个 block-receive cmd、一个 Update 循环。没有 polling、
没有 tick、idle 时 0 CPU。

### inject* 在干啥

每个 inject* 函数都是同样的形状：先告诉用户这一轮是什么触发的
（一条 notice），再把触发器的 payload 当作 user message 显示在 conv，
最后把 payload 交给 SubmitToAgent 让 agent 真正去回答。"payload" 因
生产者而异：

| 生产者 | 真正发给 agent 的内容 |
| --- | --- |
| Cron | cron 任务的 `Prompt` 字符串（用户排程时写的内容）|
| 异步 hook | context findings 与 `ContinuationPrompt` 合并成一个 user turn |
| Subagent 完成（空闲时） | `mainNotice` 的 `Content`——带着 subagent 最终输出的 `<task-notification>` |

```
每个 inject*
   ├─ conv.AddNotice(...)                          ◄── "Scheduled task fired" 等
   ├─ conv.Append(ChatMessage{Role: user, ...})    ◄── 显示在 scrollback
   └─ SubmitToAgent(core.Message)                  ◄── 启动下一回合
```

三条路径都汇聚到 **SubmitToAgent**。一样的 provider 检查、一样的
`ensureAgentSession`、一样的 `sendToAgent` 推入。**没有别的途径**
能把消息交给 agent。conversation projection 与 agent input 复用同一个
message ID；重启时按 ID 去掉待发送消息，不再靠文本内容碰运气匹配。

### 端到端走一遍：subagent 完成 → 主 agent

一个后台 subagent 完成，到它的结果进入主 agent 的下一次推理，中间发生的
就是这一串。**涉及三条 goroutine**，每条 `─ ─ ─►` 都是一次跨 goroutine
的交付。

```
   Subagent goroutine               TUI Update goroutine            主 Agent goroutine
   ──────────────────               ────────────────────            ────────────────────
   ① task 完成
       ▼
   ② notifyTaskCompleted → lifecycle handler（model_lifecycle.go）
       │   taskCompletionMessage 拼出 <task-notification>
       ▼
   ③ broker.Send(to "main") → Main 投递函数
       ▼
   ④ m.mainNotices <- n  ─ ─ ─ ─ ─►  ⑤ awaitMainNotice 解阻塞
                                       → Update → onMainNotice(n)
                                          ▼
                                       ⑥ deliverNotice(n)
                                          ├─ 空闲：injectAsNewTurn
                                          │    → SubmitToAgent
                                          └─ 在跑：sendToAgent
                                          ▼
                                       ⑦ Agent.Send → core.Agent.Append
                                          ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─►  ⑧ 在跑：SDK 在下一个
                                                                            步骤边界放进对话
                                                                          空闲：wake → Run
                                                                            开一轮
```

| 步骤 | 从哪儿到哪儿 | 机制 |
|---|---|---|
| ④ → ⑤ | subagent goroutine → TUI Update goroutine | Go chan (`m.mainNotices`) + 阻塞接收的 `tea.Cmd` |
| ⑦ → ⑧ | TUI Update goroutine → 主 agent goroutine | `sdkagent.Agent.AddMessages`（加锁的队列）+ 空闲 `Run` 用的 wake chan |

在跑的轮次最早能在"结果到达时正在执行的那个工具"之后的下一步拿到结果；
如果模型已经在写最后的回答，SDK 会多走一步，而不是结束这一轮。用户中断
（Esc）后，排队的通知不会让 agent 自己重新跑起来——它跟着下一条消息一起进去。

## Path D —— Agent → 渲染

Agent goroutine 取出排队的消息、调 LLM、流式吐 token、emit 工具调用、
emit 最终结果。每条 emission 都丢到自己的 `Outbox` channel。

```
agent goroutine                         （跑在 core.Agent.Run 里）
   │
   ▼
core.Event 进 Outbox channel
   │   事件类型：OnStart、PreInfer、OnChunk（N 个）、PostInfer、
   │   PreTool、PostTool、OnMessage、end-of-turn、AgentStop 等
   │
   ▼
ContinueOutbox tea.Cmd                  agent.go: 阻塞在 channel 上，
   │                                    读出**一条** event，返回的
   │                                    tea.Msg 里**同时**带上下一个
   │                                    ContinueOutbox cmd。Update 把
   │                                    那个 cmd 还给 framework，
   │                                    framework 又跑一次……如此往复
   │                                    直到事件停止到达。一次性
   │                                    tea.Cmd 模拟"持续监听"。
   ▼
tea.Msg（具体是某种 conv.* msg 类型）
   │
   ▼
Update → routeToSubModel                update.go
   └─ conv.Update(m, &m.conv, msg)      app/conv/update.go
         │
         │ 按事件类型分发。流式流程是：
         │
         ▼
   PreInfer                             applyPreInfer
       ├─ m.Stream.Active = true
       ├─ m.Append({Role: assistant})   空的 assistant 消息 ——
       │                                后续 chunk 会追加进它
       └─ 启动 spinner
        │
        ▼
   OnChunk（一个 token batch 来一次）   applyChunk
       ├─ m.AppendToLast(text)          扩长进行中的 message
       └─ 如果 chunk.Done && 无工具调用:
              Stream.Active = false
              rt.CommitMessages()       搬到 scrollback（见下）
        │
        ▼
   PostInfer                            applyPostInfer
       ├─ rt.OnInference(resp)         model_agent_events.go
       └─ 如果有工具调用：track 它们
        │
        ▼
   PreTool / PostTool                   工具执行流
       ├─ applyPreTool                  显示 "running tool X" spinner
       └─ applyPostTool
             └─ rt.OnToolResult(tr)     model_agent_events.go
        │
        ▼
   end-of-turn 事件                     rt.OnTurnEnd(result)
        │                               model_agent_events.go
        ├─ m.CommitMessages()           model_scrollback.go
        ├─ m.drainTurnQueues()          model_turn_queue.go（Path C）
        └─ 触发 idle hooks

   rt.OnAgentStop(err)                  本轮结束（或被取消）
   rt.HandlePermGate / HandleCompactResult / HandleTokenLimitResult ……
```

### 流式文字到底渲染在哪里

终端窗口在 session 期间有**两块表面**：

```
   终端原生 scrollback                       Bubble Tea 重绘区
   （可以往上翻看的历史；                    （底部 N 行；每次 Update
    永远不会重绘——用 tea.Println              整块重画；重绘之间内容
    一行一行写上去）                          被丢弃）
```

同一个窗口，但写入方式不同。Bubble Tea 拥有底部 N 行；上面所有内容都是
它通过 `tea.Println` 写出去的常规终端输出。

**消息流式期间**，正在生成的文字活在**重绘区**而**不**在 scrollback。
每条 OnChunk 让 `m.conv.Messages` 里最后那条 assistant message 增长，
View() 把重绘区重新画一遍，用户就看见文字逐字 token 蹦出来：

```
   ─── 终端 scrollback（frozen）──────────────────────
     user: 帮我写首关于大海的诗                       ← 已 commit
   ─── Bubble Tea 重绘区（每次 Update 重画）──────────
     assistant: 古老石上低语的浪，
                潮水▮                                  ← 在 conv.Messages 里，
                                                         Stream.Active=true,
                                                         尚未 commit
     ─────────────────────────────────────────────
     ❯ （textarea 流式期间被禁用）
```

**流完之后**（最后一条 OnChunk 携带 `Done=true` 且无工具调用），
`CommitMessages` 对完成的消息调一次 `tea.Println`。这会把这条消息
**从重绘区"抬"到上面的 scrollback**：

```
   ─── 终端 scrollback（frozen）──────────────────────
     user: 帮我写首关于大海的诗
     assistant: 古老石上低语的浪，                    ← 现在 commit 完，
                潮水退去又回涨，……                     一次性通过
                                                        tea.Println 写入
   ─── Bubble Tea 重绘区（每次 Update 重画）──────────
     （空 —— CommittedCount 追上了 len(Messages)）
     ─────────────────────────────────────────────
     ❯ 在这里输入消息……
```

防止消息**同时**出现在两边的规则在 `renderAndCommit(checkReady=true)`
里：`Stream.Active == true` 时它绝不 commit 最后一条 message。
所以流式期间该消息只在重绘区；流完，一次 `tea.Println` 搬它去
scrollback，`CommittedCount` 前进一格，重绘区不再画它。

这个边界还有一条 renderer 层的顺序约束。Bubble Tea 通常只把最新的
`View()` 放进队列，等 frame ticker 异步刷新；`tea.Println` 却会立即向
scrollback 插入内容。San 的本地 Bubble Tea 补丁会在 `insertAbove`
计算滚屏几何之前同步刷新队列中的 managed frame。没有这道屏障时，
`CommittedCount` 可能已经在逻辑 View 中隐藏内容，而缩短后的 frame
还没有真正写到终端，旧 live frame 的行就可能被永久焊进原生
scrollback。`go.mod` 中的替代依赖固定到
[`yanmxa/bubbletea@81ba608`](https://github.com/yanmxa/bubbletea/commit/81ba608a2d90bacdc115d6b3acc6b1c12cb44e5d)；
等[上游 issue #1736](https://github.com/charmbracelet/bubbletea/issues/1736)
发布等价屏障后即可移除。

工具调用的 spinner 也是同样道理：工具在跑时它在重绘区，结果到了它就
消失。

## Path E —— 流式中打断 + 续聊

用户在 agent 流式输出时按 **Esc** 或 **Ctrl+C**。Agent goroutine 不会
被销毁——只取消当前这一轮 turn；用户下一条消息通过 `Append` 接着跑同一个
会话。

```
   UI / tea.Update           Agent goroutine             Provider 流式 goroutine
   ───────────────           ───────────────             ──────────────────────

   Esc 按下                  在 ThinkAct/streamInfer     HTTP 流中，
   ──▶ handleStreamCancel    turn = &turnHandle{c,d}     EmitText(ctx, ch, …)
       │
       │ 1. Agent.InterruptTurn()
       │      ├─ interruptPending.Store(true)
       │      ├─ h := turn.Swap(nil)
       │      ├─ h.cancel()  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─▶  turnCtx.Done 触发
       │      │                                       streamInfer 返回
       │      │                                       execTools Phase 3 break
       │      │                                       EmitText select 命中
       │      │                                       ctx.Done → 不泄漏
       │      │                                       ThinkAct 返回
       │      │                                       close(h.done) ──┐
       │      └─ <-h.done   （≤ 250 ms）◀ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─┘
       │
       │ 2. conv 侧 UI 更新（仅显示，不回写 agent）
       │      ├─ Stream.Stop / 隐藏弹窗 / 清空 pending 问题
       │      ├─ cancelPendingToolCalls    → 显示已取消的 tool_result
       │      └─ MarkLastInterrupted       → assistant 末尾 ⏸ 徽章
       │
       │ 3. CommitMessages + drainInputQueueAfterCancel
       │
       │                          内循环 break（识别 turn cancel），
       │                          interruptPending.Store(false)，
       │                          emit TurnEnded{StopCanceled}
       │
       │   ◀── TurnEnded ─────────┤
       │   OnTurnEnd: StopReason==Cancelled
       │              → 跳过 idle 钩子；Stop/通知钩子都不会触发
       │
       │                          外循环：waitForInput（idle）

   用户输入 "改做 B"
   ──▶ SubmitToAgent
       └─ ensureAgentSession 检查到 Active=true —— 不重建
       └─ sendToAgent → "改做 B"
       └─ Agent.Send ──────────▶  Append + wake
                                  waitForInput 解除阻塞
                                  循环顶部：interruptPending=false → 正常进入
                                  新 turnHandle，全新 ThinkAct
                                                                ─▶ 新流
```

### 为什么 cancel 时根本不需要碰 agent 状态

handleStreamCancel **不**往 `a.messages` 里塞任何 marker，也**不**把
conv 同步回 agent——两边各自维护自己的副本和 ID。能做到这点是因为：

- **悬空的 `tool_use` 由 convert 层兜底**。如果 cancel 发生在
  `PostInfer` 已经追加了带 `tool_calls` 的 assistant 但 tool_result 还
  没全回来的时刻，下一轮推理时的 convert 会把没有匹配 result 的
  `tool_use` 剥掉（anthropic 的 `sanitizeToolResults`、openaicompat 的
  `SanitizeToolMessages`）。
- **连续 user 消息也是允许的**。Anthropic converter 自己 `mergeConsecutiveMessages`；
  OpenAI / DeepSeek / Moonshot 等都直接接受 user 接 user。
- **打断本身对模型已经可见**。被截断的 assistant 回复和已取消的
  `tool_result` 已经说明上一轮没完成，不再额外注入 reminder 或合成消息。

三件套撑起整个 cancel 的安全性：

| 机制 | 它在保护什么 |
|---|---|
| `turn atomic.Pointer[turnHandle]` | "当前活动 turn 的手柄"。`Swap(nil)` 让 cancel 原子化，避免两次打断重复 cancel 下一轮。 |
| `interruptPending atomic.Bool` | "两轮之间"打断的备忘录（此刻 `turn` 短暂为 nil），Run 内循环下次开头读到则直接 break 回 `waitForInput`，不会偷跑一轮 ThinkAct。 |
| `turnHandle.done` chan + 250 ms 上限 | UI 等 ThinkAct 真正 unwind 后再返回用户——避免 spinner / modal 状态和 agent 最后几个 ctx-cancelled 的 emit 抢着收尾。上限是给"不响应 ctx 的工具"留的保险；正常情况微秒级。 |

为什么和旧实现差别大：旧的 cancel 路径直接 `Agent.Stop`，杀掉 goroutine，
下一条用户消息时整个 agent 重建一遍——一次 `buildAgent`、一份新的
`llm.Client`、session 里多两条 Stop/Start 事件。新路径 agent 不重建，
下一次 `Agent.Send` 就是简单一次 `Append`，LLM 服务端看到相同的
prompt 前缀（prompt cache 命中更稳）。

## 文件指路

| Path 步骤 | 文件 |
|---|---|
| `Update` 派发 | [`internal/app/update.go`](../../internal/app/update.go) |
| 键盘处理 | [`internal/app/update_keys.go`](../../internal/app/update_keys.go) |
| Submit + SubmitToAgent | [`internal/app/update_submit.go`](../../internal/app/update_submit.go) |
| Slash 命令 controller | [`internal/app/input/slash_command.go`](../../internal/app/input/slash_command.go) |
| Slash 命令 env 装配 | [`internal/app/update_command.go`](../../internal/app/update_command.go) |
| inject 路径（cron/hook/通知）| [`internal/app/model_turn_queue.go`](../../internal/app/model_turn_queue.go) |
| Agent 事件回调 | [`internal/app/model_agent_events.go`](../../internal/app/model_agent_events.go) |
| Scrollback commit | [`internal/app/model_scrollback.go`](../../internal/app/model_scrollback.go) |
| Conv 事件路由 | [`internal/app/conv/update.go`](../../internal/app/conv/update.go) |
| `agent.Send` / outbox 轮询 | [`internal/app/agent.go`](../../internal/app/agent.go) |
| 流式中断处理 | [`internal/app/update_input_effects.go`](../../internal/app/update_input_effects.go) |
| `InterruptTurn` | [`internal/agent/session.go`](../../internal/agent/session.go) |
| `turn` / `InterruptCurrentTurn` / Run loop | [`internal/core/run.go`](../../internal/core/run.go) |
| 底部 UI 组合 | [`internal/app/view.go`](../../internal/app/view.go) |
