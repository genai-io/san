# Task Management

The task management system lets the LLM create, track, and coordinate multi-step work items. Tasks flow through `pending → in_progress → completed` with dependency support and real-time TUI rendering.

## Core Loop

```
┌─────────────┐  tool schemas  ┌─────────┐  ToolCalls    ┌──────────────┐
│  schema.go  │───────────────►│   LLM   │──────────────►│  toolexec.go │
│  + prompts  │                │         │               │  (dispatch)  │
└─────────────┘                └────▲────┘               └──────┬───────┘
                                    │                           │
                           results  │                    Registry.Execute()
                                    │                           │
                             ┌──────┴───────┐            ┌──────▼──────┐
                             │  handleTool  │◄───────────│  Todo Tools │
                             │  Result()    │  ToolResult │  C/R/U/L   │
                             └──────┬───────┘            └──────┬──────┘
                                    │                           │
                             continueWith                 read / write
                             ToolResults()                      │
                                    │                    ┌──────▼──────┐
                             ┌──────▼───────┐   List()   │  TodoStore  │
                             │  Next LLM    │            │ (singleton) │
                             │  Turn        │            └──────┬──────┘
                             └──────────────┘                   │
                                                         every 80ms
                                                                │
                             ┌──────────────┐   List()   ┌──────▼──────┐
                             │   Terminal   │◄───────────│   View()    │
                             │   Output     │  render    │ todolist.go │
                             └──────────────┘            └─────────────┘
```

**Two independent data paths share one store:**
- **Write path**: LLM → ToolCall → Todo Tools → `DefaultTodoStore` mutation
- **Read path**: Bubble Tea TickMsg → `View()` → `DefaultTodoStore.List()` → render

No explicit notification needed. Spinner TickMsg (80ms) triggers `View()` re-render,
which always reads fresh state from the singleton store.

## Task Status Flow

```
            TaskCreate()              TaskUpdate(status=...)
                │                     ┌────────────────────┐
                ▼                     │                    │
          ┌──────────┐          ┌─────▼──────┐      ┌─────▼──────┐
          │ pending  │─────────►│in_progress │─────►│ completed  │
          │    ☐     │          │    ⠋       │      │    ✓       │
          └────┬─────┘          └────────────┘      └────────────┘
               │
          if BlockedBy                        TaskUpdate(status="deleted")
          has open tasks                              │
               │                              ┌───────▼──────┐
               ▼                              │   deleted    │
          ┌──────────┐                        │  (hidden)    │
          │ blocked  │                        └──────────────┘
          │    ▸     │
          └──────────┘
```

## Dependency Mechanism

```
TaskUpdate(taskId="2", addBlockedBy=["1"])
TaskUpdate(taskId="4", addBlockedBy=["2","3"])

  #1 Set up database
   │
   ├─ blocks ─► #2 Create API ──┐
   │                             ├─ blocks ─► #4 Integration tests
   └─ blocks ─► #3 Add auth  ──┘
```

**Storage**: each task holds `Blocks []string` and `BlockedBy []string` (bidirectional).

**Blocked detection** (renderTodoTask): iterate `task.BlockedBy`, check if any blocker
is NOT completed → show `▸` instead of `☐`.

**Auto-unblock**: no explicit event. When a blocker completes, the next `View()` frame
re-evaluates and the dependent task icon changes `▸ → ☐` automatically.

```
 #1 completes           #2,#3 complete
 ✓ Set up database      ✓ Set up database
 ▸ Create API  → ☐      ✓ Create API
 ▸ Add auth    → ☐      ✓ Add auth
 ▸ Integration   ▸      ▸ Integration  → ☐    (now unblocked)
```

## Agent Loop vs UI Rendering

```
Agent Loop (write)                    TUI View() (read)
──────────────────                    ─────────────────
LLM → TaskCreate ──┐                  View()
LLM → TaskUpdate ──┼──▶ DefaultTodoStore ◀── renderTodoList()
LLM → TaskList  ───┘     (singleton)          ↑
                                          Spinner Tick (80ms)
                                          triggers View() re-render
```

**Tools the LLM calls**: primarily `TaskCreate` (create) and `TaskUpdate` (status progression).
Typically calls `TaskList` once at the end to confirm final state. `TaskGet` is available but
rarely invoked proactively.

**UI rendering is fully independent of the agent loop**:
- Tool calls execute through the agent loop, directly mutating `DefaultTodoStore`
- Bubble Tea calls `renderTodoList()` on every `View()`, reading `DefaultTodoStore.List()`
- Both sides communicate through the shared singleton — no message passing needed
- Spinner tick (80ms) drives periodic `View()` re-renders, picking up latest data naturally

**Auto-cleanup on completion**: when all tasks are completed and LLM is idle (`!m.streaming`),
`renderTodoList()` calls `DefaultTodoStore.Reset()` to clear the store. The task list disappears
from the UI and the next round of tasks starts with ID 1.

## UI Layout

```
 ◆ ⠧ Thinking...                    ← activeContent (streaming)

   Tasks 1/4                         ← renderTodoList() output
   ✓ Set up database                    shows all tasks (incl. completed)
   ⠧ Create API endpoints
     Creating API endpoints          ← activeForm (2nd line)
   ☐ Add auth
   ▸ Integration tests               ← blocked
 ────────────────────────────────────
 ❯ _                                 ← input area
 ────────────────────────────────────

 all completed + LLM idle → Reset() → list disappears
```

## Store Reset

```
all completed + idle  →  renderTodoList() calls Reset()  →  tasks={}, nextID=1
/clear command        →  DefaultTodoStore.Reset()        →  tasks={}, nextID=1
```

## Key Design Choices

| Choice | Why |
|--------|-----|
| Global singleton | Tools and TUI share state without coupling; matches `DefaultRegistry` pattern |
| No push notification | `View()` polls store every frame — simple, no channels/messages needed |
| Functional options | `Update(id, WithStatus(...), WithOwner(...))` — composable, type-safe |
| Blocked = runtime check | No state machine; `renderTodoTask` evaluates `BlockedBy` on each frame |
| `parseToolInput("")` → `{}` | LLM sends empty body for `TaskList()`; must not error |

## See Also

- [Subagent System](subagent-system.md) — Task tools used within agent workflows
- [System Prompt](system-prompt.md) — How agent context is constructed
