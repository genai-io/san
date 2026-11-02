# ACP implementation plan

Design: [PROP-0004](../../docs/design/proposals/0004-acp-agent-entrypoint.md).

- [x] **Transport kernel:** self-owned `sdk-go/pkg/acp`, bounded NDJSON,
  bidirectional calls, ordered notifications, cancel/EOF/Close/Wait tests.
- [x] **Typed agent:** pinned ACP v1 wire types, ordered admission/cancel and
  response barriers ([sdk-go#97](https://github.com/genai-io/sdk-go/pull/97),
  [sdk-go#98](https://github.com/genai-io/sdk-go/pull/98)); native Go tests.
- [x] **Interaction lifecycle:** `internal/interaction` owns request queuing,
  validity checks and cancellation; presentation remains in app.
- [ ] **Shared execution:** extract input and turn policy/lifecycle without losing
  the proposal's capability map, worker interactions, task fencing or native state.
  Determine remaining package boundaries from the full execution flow.
  - [x] Execution recording: `session.Session.RecordEvent` carries essential
    producer write failures into turn receipts; hook/skill audit callbacks are
    session-owned and used by TUI. Consumer acknowledgement, permission policy,
    input admission and queue/cancel fencing remain in their existing owners.
    Covered by session execution-recording tests and TUI finalization tests.
  - [x] Native input delivery: `agent.InputQueue` owns FIFO retry retention and
    user-only recall dispatch; TUI retains admission and lifecycle gates.
    Covered by agent input tests and existing TUI queue/cancellation tests.
  - [x] Cancellation admission: `agent.InputQueue` owns manual-stop holds and
    matching consumer-finalization fences, enforced by delivery itself. Native
    queue Enter still validates the selected item; compaction and hooks stay in app.
    Covered by agent admission tests and native cancellation/queue integration tests.
  - [x] Prompt admission: `agent.CheckPrompt` owns UserPromptSubmit decisions and
    additional-context staging using the existing hook engine and reminder owner.
    TUI keeps its fail-open hook budget and admission call sites; blocked context
    remains pending for later delivery. Midstream delivery is not newly gated.
    Covered by native policy and TUI dispatch/queue tests; no ACP adapter yet.
  - [x] Human permissions: shared preparation and resolution in `internal/interaction`,
    wired into TUI; see [ownership contract](../../docs/reference/dependency-rules.md#rules).
  - [x] Foreground result policy: `agent.PostTool` dispatches workspace
    handoffs and PostToolUse/Failure hooks in native order. TUI keeps outbox/cancel
    admission, handoff consumption and workspace callbacks; worker-local handoffs,
    model-result paging, recording and compaction remain unchanged. Native tests
    cover effect/hook payloads and ordering, hook errors and stale/cancelled results.
  - Native prerequisite: delayed Stop-hook outcomes are fenced to their idle
    decision, opened session and runtime outbox, and discarded after input,
    cancellation or teardown. Native command/dispatch regressions cover stale
    blocks and successes. This is not shared completion: normal receipts still
    exclude idle-hook persistence, and failure/autopilot policy remains in app.
  - [x] Normal completion: shared record-before-continue and Stop decisions;
    see [completion policy](../../docs/packages/2-feature/agent.md).
  - [x] Compaction policy: shared manual summarization/hooks, inactive boundary
    recording and post-compaction context reset; see [agent lifecycle](../../docs/packages/2-feature/agent.md#lifecycle).
    TUI retains admission/projection/queue release; manual admission invalidates
    pending Stop decisions. Automatic summarization and worker compaction are unchanged.
  - [x] Reminder state: managed by `internal/reminder`, reused by TUI.
- [ ] **Adapter:** add standard/optional projections first, then negotiated task
  controls and command/config management; explicitly gate unfinished capabilities.
- [ ] **Acceptance:** baseline-only, richer ACP and San-extension profiles;
  cover question/approval cancellation, visible background completion held from
  main until user continuation, and same-host versus restarted replay.
  Test against independent clients; verify Zed/Orca separately and measure resources.

Kernel review: [sdk-go#93](https://github.com/genai-io/sdk-go/pull/93).
No release or San dependency change yet; stop if this needs a broad TUI rewrite.
