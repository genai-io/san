# ACP implementation plan

Design: [PROP-0004](../../docs/design/proposals/0004-acp-agent-entrypoint.md).

- [x] **Transport kernel:** self-owned `sdk-go/pkg/acp`, bounded NDJSON,
  bidirectional calls, ordered notifications, cancel/EOF/Close/Wait tests.
- [ ] **Typed agent:** pin official ACP v1 schema; add generated wire types,
  session admission and cancellation, then verify with an independent client.
- [ ] **Shared host:** extract session/interaction policy and lifecycle effects;
  preserve native TUI behavior, recording order and task-recovery semantics.
- [ ] **Adapter:** add opt-in `san acp`; reuse saved configuration and enforce scope.
- [ ] **Acceptance:** edit, approve/deny, cancel and resume in Zed; compare native
  history/permissions and measure latency/memory. Orca registration is separate.

Kernel review: [sdk-go#93](https://github.com/genai-io/sdk-go/pull/93).
No release or San dependency change yet; stop if this needs a broad TUI rewrite.
