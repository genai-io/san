# ACP implementation plan

Design: [PROP-0004](../../docs/design/proposals/0004-acp-agent-entrypoint.md).

- [x] **Transport kernel:** self-owned `sdk-go/pkg/acp`, bounded NDJSON,
  bidirectional calls, ordered notifications, cancel/EOF/Close/Wait tests.
- [x] **Typed agent:** pinned ACP v1 wire types, ordered admission/cancel and
  response barriers ([sdk-go#97](https://github.com/genai-io/sdk-go/pull/97),
  [sdk-go#98](https://github.com/genai-io/sdk-go/pull/98)); native Go tests.
- [x] **Interaction lifecycle:** `internal/interaction` owns request queuing,
  validity checks and cancellation; presentation and authorization remain in app.
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
  - [x] Reminder conversation policy: `reminder.Service` owns first-input emission,
    native-history detection and reset/compaction rearming; TUI retains lifecycle gates.
    Covered by reminder lifecycle, rejected-input retry and native TUI tests.
- [ ] **Adapter:** add standard/optional projections first, then negotiated task
  controls and command/config management; explicitly gate unfinished capabilities.
- [ ] **Acceptance:** baseline-only, richer ACP and San-extension profiles;
  cover question/approval cancellation, visible background completion held from
  main until user continuation, and same-host versus restarted replay.
  Test against independent clients; verify Zed/Orca separately and measure resources.

Kernel review: [sdk-go#93](https://github.com/genai-io/sdk-go/pull/93).
No release or San dependency change yet; stop if this needs a broad TUI rewrite.
