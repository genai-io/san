# 渲染：Model → 终端

> English version: [`rendering.md`](rendering.md)

[`data-flow.zh.md`](data-flow.zh.md) 的姊妹篇。数据流转讲的是输入怎么变成
状态；本文讲的是状态怎么变成屏幕上的字符。

## 两条渲染通路，同一套渲染函数

TUI 里每一个可见字节都由 `internal/app/conv/view.go` 里的代码产生 ——
`RenderMessageAt`、`RenderUserMessage`、`RenderAssistantMessage`、
`RenderToolCalls` 等等。但**它们的输出被两套完全不同的机制消费**：

```
┌─ internal/app/conv 里的渲染原语 ──────────────────────────┐
│                                                           │
│   RenderMessageAt   ─┬─ 通过 MDRenderer 渲染 markdown      │
│   RenderUserMsg     ├─ 工具调用 + 结果块                   │
│   RenderToolCalls   ├─ 系统 notice                         │
│   ...               └─ Spinner / 进度指示                  │
│                                                           │
└────────────────┬──────────────────┬──────────────────────┘
                 │                  │
                 ▼                  ▼
   ┌────────────────────┐   ┌──────────────────────┐
   │  CommitMessages    │   │  View()              │
   │  → tea.Println     │   │  → bubbletea 重绘     │
   │                    │   │                      │
   │  只画已 commit 的   │   │  活动内容             │
   │  消息（Stream.     │   │  （未 commit 消息 +    │
   │  Active==false）   │   │   spinner + 模态 +    │
   │                    │   │   输入条）             │
   └────────────────────┘   └──────────────────────┘
                 │                  │
                 ▼                  ▼
   ┌────────────────────┐   ┌──────────────────────┐
   │ 终端原生 scrollback  │   │ 底部 N 行             │
   │ （一旦写入就冻结）   │   │ （每次 Update 重画）  │
   └────────────────────┘   └──────────────────────┘
```

这种拆分使得**流式文字和工具 spinner 可以实时刷新（在重绘区）**，
而已完成的消息**冻结在 scrollback 里**——用户能向上滚动查看。

## View() 组合的是什么

`(*model).View()` 在 `internal/app/view.go` 里，每次 `Update` 之后都会
被调用。它从三种顶层布局中挑一种：

```
View() 自顶向下决定用哪种布局：

  1. 有 popup 活动？           ──► 全屏渲染那个 popup
     （/model 选择器、/tools 选择器等 —— 见 data-flow Path A）

  2. 有 modal 活动？           ──► 在分隔符之间渲染 modal
     （Question modal、Approval modal）

  3. 否则（普通模式）：
        chat section         ◄── RenderActiveContent
        本回合 token 用量
        分隔符
        队列预览              ◄── 流式期间用户排队的输入
        textarea             ◄── m.userInput.RenderTextarea()
        suggestion overlay   ◄── 自动补全面板
        分隔符
        status line          ◄── 模型名、token、模式
```

Popup 和 modal 概念相同（都是"弹出来挡住输入的 UI"），但渲染流程不同：
modal 上下夹着 chat 内容；popup 直接占满屏幕。

## 单条消息是怎么渲染的

`RenderMessageAt(params, idx, isStreaming)` 按 `msg.Role` 分发：

```
              ┌─ Role: User ─┐
              │              │
              │  含 ToolResult?   ──► RenderToolResultInline
              │  否则             ──► RenderUserMessage
              │                        （文字 + 图片，md 渲染）
              │
RenderMessageAt
              │
              ├─ Role: Notice ──► RenderSystemMessage
              │                   （纯文本，灰色）
              │
              └─ Role: Assistant ──► renderAssistantWithTools
                                      ├─ 正文 + thinking（markdown）
                                      ├─ 工具调用块
                                      └─ 配对的工具结果
```

`renderAssistantWithTools` 会从当前消息往后扫，收集紧随其后的
ToolResult 消息（这些是这一轮 assistant 的工具调用结果），然后把它们
作为一个统一的"工具调用块"渲染在助手正文下方。

## Markdown 渲染（MDRenderer）

`internal/app/conv/markdown.go` 包了一层
[glamour](https://github.com/charmbracelet/glamour)，并做了两类定制：

| 关注点 | MDRenderer 做了什么 |
| --- | --- |
| 宽度 | 根据终端宽度构建（`width − 4` 是为了让 "● " 前缀留出位置）。`ResizeMDRenderer` 在 `WindowSizeMsg` 时重建。 |
| 背景 | 自动检测深色/浅色终端。每次 `Render` 都 `rebuildIfNeeded()` 检查切换。 |
| 表格 | 在 glamour 看到之前抽出来；用 lipgloss 的 table 原语渲染，边框可控。 |
| 软换行 | LLM 经常在 ~80 列硬换行；渲染器先把软换行合并成段落，让 glamour 按真实宽度再换行。 |
| 内联标记 | 自定义内联 markdown 处理 glamour 渲染不好的部分（如嵌套格式里的反引号）。 |

为什么宽度对渲染重要：glamour 根据配置宽度计算列宽并换行。终端 resize
之后，旧宽度下渲染的内容**还留在 scrollback 里**，但下方重绘区已经用了
新宽度——这就是"resize 后 scrollback 看起来错位"的根源，也是
`handleWindowResize` 要调用 `reflowScrollback` 的原因：
**把每一条已 commit 消息用新宽度重新渲染，清屏、重新写进 scrollback**。

## 工具调用 + 结果

`internal/app/conv/tool_render.go` 渲染助手的工具调用：

```
● Bash(npm test)                        ← 工具名 + 摘要参数
    ⎿  > vitest run                     ← 折叠的结果预览
        ✓ src/foo.test.ts (12)
        ✓ src/bar.test.ts (8)
       … 47 more lines (Ctrl-O to expand)
```

驱动渲染的状态：

- **Pending vs done**：每个工具调用在 `m.conv.Tool.PendingCalls` 里
  直到对应的 `ToolResult` 到达。pending 期间，工具名旁显示 spinner。
- **Expanded / collapsed**：消息级别的 `Expanded` 标志，由 Ctrl-O 切换。
  折叠时显示预览 + 行数；展开时显示全部内容。
- **错误**：`ToolResult.IsError` 翻转图标（✓ → ✗）并把结果染色。
- **并行模式**：多个工具调用并发跑时，布局改变（每个调用独立显示进度）。

## 活动内容 vs scrollback —— 同一套渲染器，两个消费者

活动内容区（中部，每次 Update 重绘）和 scrollback（上方，`tea.Println`
写入）都用 `RenderMessageAt`。区别在于**它们渲染的消息范围不同**：

```
m.conv.Messages = [ msg0, msg1, msg2, msg3, msg4, msg5 ]
                                          ▲
                                          CommittedCount=4

scrollback（已写好）：       msg0, msg1, msg2, msg3
活动内容（View 重绘）：       msg4, msg5    + 如有 spinner
                            ──────────
                            如果 msg5 是流式中的 assistant message，
                            RenderMessageRange 会传 isStreaming=true，
                            渲染器显示光标、跳过"完成"后缀。
```

`CommitMessages` 推进 `CommittedCount` 并对每条新 commit 的消息
发一次 `tea.Println`——同一段渲染出的字符串就从"活动内容"**搬**进了
"scrollback"。用户看到的是一次视觉过渡，不是重复显示。

## Resize 行为

终端 resize 是唯一会让**已经画到 scrollback 里的内容失效**的事件
（因为 glamour 的换行是按宽度算的）。
`internal/app/update_resize.go` 里的 `handleWindowResize`：

1. 更新 `m.env.Width / Height` 和 textarea 宽度
2. 调 `m.conv.ResizeMDRenderer(newWidth)` 重建 glamour
3. 如果宽度真变了且已经有 commit 的消息：
   `reflowScrollback` 清屏，用新宽度对每条 commit 消息重新
   `tea.Println` 一次
4. 返回 cmd；bubbletea 接着调 View() 用新宽度重画底部条

## 文件指路

| 关注点 | 文件 |
| --- | --- |
| `View()` 组合 | [`internal/app/view.go`](../../internal/app/view.go) |
| 单消息渲染 | [`internal/app/conv/view.go`](../../internal/app/conv/view.go) |
| User / Assistant / Notice 渲染 | [`internal/app/conv/message.go`](../../internal/app/conv/message.go) |
| Markdown 渲染 | [`internal/app/conv/markdown.go`](../../internal/app/conv/markdown.go) |
| 工具调用 / 结果渲染 | [`internal/app/conv/tool_render.go`](../../internal/app/conv/tool_render.go) |
| Compact / 进度 / tracker 渲染 | [`internal/app/conv/compact.go`](../../internal/app/conv/compact.go)、[`progress.go`](../../internal/app/conv/progress.go)、[`tracker_view.go`](../../internal/app/conv/tracker_view.go) |
| `MDRenderer` 生命周期 | [`internal/app/conv/model.go`](../../internal/app/conv/model.go) |
| Scrollback commit | [`internal/app/model_scrollback.go`](../../internal/app/model_scrollback.go) |
| Resize + reflow | [`internal/app/update_resize.go`](../../internal/app/update_resize.go) |
