# ACP implementation plan

Design: [PROP-0004](../../docs/design/proposals/0004-acp-agent-entrypoint.md).

- [x] **Transport kernel:** self-owned `sdk-go/pkg/acp`, bounded NDJSON,
  bidirectional calls, ordered notifications, cancel/EOF/Close/Wait tests.
- [ ] **Typed agent:** pin official ACP v1 schema; add generated wire types,
  ordered admission/cancel and response barriers; test against an independent client.
- [ ] **Shared host:** extract policy/lifecycle without losing the proposal's
  capability map, worker interactions, task fencing or native state.
- [ ] **Adapter:** add standard/optional projections first, then negotiated task
  controls and command/config management; explicitly gate unfinished capabilities.
- [ ] **Acceptance:** baseline-only, richer ACP and San-extension profiles;
  cover question/approval cancellation, visible background completion held from
  main until user continuation, and same-host versus restarted replay.
  Verify Zed/Orca separately; measure resources.

Kernel review: [sdk-go#93](https://github.com/genai-io/sdk-go/pull/93).
No release or San dependency change yet; stop if this needs a broad TUI rewrite.
