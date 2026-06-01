# 数据流详解

> 完整英文原文：[`docs/concepts/data-flow.md`](../concepts/data-flow.md)
>
> 渲染伴侣文档：[`rendering.md`](../concepts/rendering.md) — 渲染输出的实际组成

本文追踪一个按键（或 cron 触发、或 hub 事件）如何穿越 TUI，最终成为用户看到的 Agent 响应。

---

## 核心角色

TUI 是基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea) 的 MVU 循环。三种 Bubble Tea 原语驱动一切：

- **`tea.Msg`** — 进入模型的事件（按键、窗口调整、微调器滴答、自定义进程内消息）
- **`Update(msg)`** — 纯函数，变异模型并返回 `tea.Cmd`
- **`tea.Cmd`** — 框架运行的函数；返回值作为新 `tea.Msg` 注入回 Update

关键约定：许多内部处理器返回 `(tea.Cmd, bool)`。`true` 表示"我认领了此事件"——停止传播链；`false` 允许调用者尝试下一层。

---

## 总览图

```
   ┌──────────────────────────────────────────────────────┐
   │  输入源                                              │
   │                                                      │
   │   键盘       斜杠命令       cron       异步钩子       │
   │     │            │           │            │          │
   │     ▼            ▼           ▼            ▼          │
   │  handleSubmit → SlashCtrl  inject*     inject*       │
   │     │            │           │            │          │
   │     └────────────┼───────────┴────────────┘          │
   │                  ▼                                   │
   │           SubmitToAgent(content, images)             │
   │                  │                                   │
   │                  ▼ agent.Send（推送到 Inbox）         │
   └──────────────────┼───────────────────────────────────┘
                      │
   ┌──────────────────┼───────────────────────────────────┐
   │  Agent 循环       ▼                                   │
   │       ┌─────────────────────┐                        │
   │       │  Inbox → LLM → Tool │   ← 在 goroutine 中运行│
   │       │     ↘    ↙          │                        │
   │       │     Outbox          │ → core.Event 流        │
   │       └─────────────────────┘                        │
   └──────────────────┼───────────────────────────────────┘
                      │
   ┌──────────────────┼───────────────────────────────────┐
   │  渲染             ▼                                   │
   │         ContinueOutbox tea.Cmd                       │
   │                  │                                   │
   │                  ▼ tea.Msg                           │
   │              Update → conv.Update → 回调             │
   │                  │                                   │
   │                  ▼                                   │
   │         CommitMessages → tea.Println → 滚动回看      │
   │                  │                                   │
   │                  ▼                                   │
   │               View() → 底部 UI 条                    │
   └──────────────────────────────────────────────────────┘
```

---

## 路径 A — 文本输入

用户输入 `hello`，按 **Enter**。

### 按键级路由

```
tea.KeyMsg('h') ──── 每个按键
   │
   ▼
Update                              update.go
   │
   ├─ case tea.KeyMsg → routeKeypress
   │     │
   │     ├─ tryActivePopup          — 问题弹窗、批准弹窗、
   │     │                            斜杠命令选择器
   │     │                            输入 'h' 时无活动弹窗
   │     │
   │     ├─ HandleImageSelectKey    — 图片选择模式（关闭）
   │     ├─ HandleSuggestionKey     — 提示建议模式（关闭）
   │     ├─ HandleQueueSelectKey    — 队列导航模式（关闭）
   │     │
   │     └─ handleTextareaShortcut  — Ctrl 快捷键 / Tab / Enter / ...
   │           └─ KeyRunes('h') 不匹配 → (nil, false)
   │
   ├─ routeToSubModel               — 无子模型认领 KeyRunes 消息
   │
   └─ updateTextarea                — textarea 消费字符
   ▼
View                                view.go   底部 UI 显示 "h▮"
```

### Enter 提交

```
tea.KeyMsg(Enter)
   │
   ▼
routeKeypress → handleTextareaShortcut
   └─ case tea.KeyEnter → m.handleSubmit()
        │
        ▼
   handleSubmit
        步骤 1：读取 textarea ───► "hello"
        步骤 2：流式活跃？──────► 否
        步骤 3：→ dispatchSubmission("hello")
                  │
                  ▼
   dispatchSubmission
        步骤 1：是 "exit"？─────► 否
        步骤 2：提示钩子 ────────► 允许
        步骤 3：记录到历史
        步骤 4：是斜杠命令？────► 否（无前导 "/"）
        步骤 5：发送到 Agent
                  ├─ buildUserMessage("hello")
                  ├─ conv.Append(msg)         ← 用户可见
                  ├─ userInput.Reset()
                  └─ SubmitToAgent(msg.Content, msg.Images)
                        │
                        ▼
   SubmitToAgent
        ├─ 提供商已连接？──── 是
        ├─ ensureAgentSession()  按需启动 Agent goroutine
        ├─ sendToAgent ────────► agent.Task inbox channel
        │
        └─ 返回 ContinueOutbox cmd
```

---

## 路径 B — 斜杠命令

用户输入 `/clear`，按 **Enter**。路径与 A 重叠至步骤 4：

```
handleSubmit → dispatchSubmission
   步骤 1..3 同路径 A
   步骤 4：runSlashCommandIfMatched("/clear")
              │
              ▼
   SlashCommandController.HandleSubmit
              │ ParseCommand("/clear") → ("clear", "")
              ▼
   handleClearCommand(c, ctx, "")
        ├─ env.StopAgentSession()
        ├─ env.PersistSession()
        ├─ env.Conversation.Clear()
        ├─ env.Input.Reset()
        └─ returns (result="conversation cleared", ...)
              │
              ▼
   c.env.Conversation.AddNotice(result)
   c.env.CommitMessages() → tea.Println → 滚动回看
```

---

## 路径 C — 后台触发器

三个生产者可以在无用户输入时运行。它们将输出停放在队列/通道中，然后在 **轮次边界**（Agent 轮次结束的那一刻）排空。

### 三个生产者

| 生产者 | 位置 | 停放位置 |
|--------|------|----------|
| Cron 触发 | trigger.StartCronTicker（后台 goroutine） | m.systemInput.CronQueue |
| 异步钩子跟进 | trigger.StartAsyncHookTicker | m.systemInput.AsyncHookQueue |
| 子 Agent 完成 | agent.SetLifecycleHandler → hub.Publish | m.mainEvents（Go channel） |

### 轮次边界排空

当活动 Agent 完成一轮时，`OnTurnEnd` 调用 `drainTurnQueues`：

```
OnTurnEnd
   └─ drainTurnQueues
        优先级 高 → 低，取第一个非空：
        │
        ├─ 用户输入队列？─── 轮次流式期间停放
        ├─ cron 队列？────► injectCronPrompt(prompt)
        ├─ 异步钩子队列？──► injectAsyncHookContinuation(item)
        └─ agentEventHub 批次 ► injectNotification(merged hub.Message)
```

### 空闲时的唤醒

`drainTurnQueues` 仅在 `OnTurnEnd` 时触发。在轮次之间到达的事件（如子 Agent 完成后几分钟）通过**阻塞接收 `tea.Cmd`** 唤醒 Update 循环：

```
Init
   └─ awaitMainEvent(m.mainEvents)
        └─ 阻塞在 channel 上，事件到达时产生 mainEventMsg{event}

Update
   case mainEventMsg:
        └─ onMainEvent(ev)
              ├─ 追加到 m.pendingMainEvents
              ├─ 重新启动 awaitMainEvent
              └─ 如果 !Stream.Active：
                   立即注入通知
```

---

## 路径 D — Agent → 渲染

Agent goroutine 运行收件箱，调用 LLM，流式输出 Token，发出工具调用，发出最终结果。每次发出都进入其 `Outbox` 通道。

```
Agent goroutine
   │
   ▼
core.Event → Outbox channel
   │
   ▼
ContinueOutbox tea.Cmd ── 阻塞在通道上读取一个事件，
   │                      作为携带下一个 ContinueOutbox cmd 的
   │                      tea.Msg 返回。所以 Update 持续调度
   │                      新轮询直到事件停止。
   ▼
tea.Msg (conv.*)
   │
   ▼
Update → routeToSubModel
   └─ conv.Update(m, &m.conv, msg)
         │
         ▼
   PreInfer──► rt.OnTurnBegin()，重置 Token 计数器
         │
         ▼
   OnChunk──► m.AppendToLast(text) 增长进行中的消息
         │
         ▼
   PostInfer──► rt.OnTokenUsage(resp)
         │
         ▼
   PreTool / PostTool──► 工具执行展示
         │
         ▼
   轮次结束事件──► rt.OnTurnEnd(result)
         ├─ m.CommitMessages()
         ├─ m.drainTurnQueues()
         └─ 触发空闲钩子
```

### 流式渲染的双面模型

```
   ─── 终端滚动回看（冻结）────────────────
     user: write a poem about the sea      ← 已提交
   ─── Bubble Tea 重绘区（重绘）────────────
     assistant: Whispers of waves on▮     ← 在 conv.Messages 中，
                                              Stream.Active=true，
                                              尚未提交
     ────────────────────────────────────
     ❯ (textarea 等待，在流式过程中禁用)
```

**流结束时**，`CommitMessages` 调用 `tea.Println` 将完整消息从重绘区提升到滚动回看区。

---

## 路径 E — 中断与恢复

用户按 **Esc** 或 **Ctrl+C** 中断正在流式的 Agent：

```
Esc 按键
   ──▶ handleStreamCancel
       │
       │ 1. Agent.InterruptTurn()
       │      ├─ interruptPending.Store(true)
       │      ├─ h := turn.Swap(nil)
       │      ├─ h.cancel()  ─ ─ ▶  turnCtx.Done() 触发
       │      │                    streamInfer 返回
       │      │                    ThinkAct 返回
       │      │                    close(h.done)
       │      └─ <-h.done   (≤ 250 ms)
       │
       │ 2. Reminder.Enqueue(InterruptReminder)
       │      └─ "上一次回复被用户中断。"
       │
       │ 3. conv 侧 UI 更新（仅显示 — 从不同步到 Agent）
       │      ├─ Stream.Stop
       │      └─ MarkLastInterrupted  → ⏸ 中断标记
       │
       │ 4. CommitMessages + drainInputQueueAfterCancel

   用户输入 "do B instead"
   ──▶ SubmitToAgent
       └─ ensureAgentSession 看到 Active=true — 不重建
       └─ sendToAgent → attachPendingReminders
                        → "<system-reminder>上一次回复被中断…</system-reminder>do B"
       └─ Agent.Send ────────▶ inbox
                               waitForInput 解除阻塞
                               新的 ThinkAct
                                              ─▶ 新的流
```

### 中断保护机制

| 机制 | 保护内容 |
|------|----------|
| `turn atomic.Pointer[turnHandle]` | 活动轮次句柄 — `Swap(nil)` 使中断原子化 |
| `interruptPending atomic.Bool` | 锁存在轮次间到达的中断 |
| `turnHandle.done` chan + 250ms 超时 | 让 UI 等待 ThinkAct 实际展开 |

---

## 关键文件索引

| 路径步骤 | 文件 |
|----------|------|
| Update 分发 | [`internal/app/update.go`](../../internal/app/update.go) |
| 键盘处理 | [`internal/app/update_keys.go`](../../internal/app/update_keys.go) |
| 提交 + SubmitToAgent | [`internal/app/update_submit.go`](../../internal/app/update_submit.go) |
| 斜杠命令控制器 | [`internal/app/input/slash_command.go`](../../internal/app/input/slash_command.go) |
| 注入路径 | [`internal/app/model_turn_queue.go`](../../internal/app/model_turn_queue.go) |
| Agent 事件回调 | [`internal/app/model_agent_events.go`](../../internal/app/model_agent_events.go) |
| 滚动回看提交 | [`internal/app/model_scrollback.go`](../../internal/app/model_scrollback.go) |
| 对话事件路由 | [`internal/app/conv/update.go`](../../internal/app/conv/update.go) |
| Agent 发送/出站轮询 | [`internal/app/agent.go`](../../internal/app/agent.go) |
| 流式中断取消 | [`internal/app/update_input_effects.go`](../../internal/app/update_input_effects.go) |
| InterruptTurn | [`internal/agent/session.go`](../../internal/agent/session.go) |
| Run 循环 | [`internal/core/agent_impl.go`](../../internal/core/agent_impl.go) |
| 底部 UI 组成 | [`internal/app/view.go`](../../internal/app/view.go) |
