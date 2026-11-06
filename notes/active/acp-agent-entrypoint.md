# ACP implementation plan

Design: [PROP-0004](../../docs/design/proposal-0004-acp-agent-entrypoint.md).

## Current scope

- SDK owns typed ACP v1 framing, admission, cancellation and response barriers.
- San composes native foreground sessions, shared input/permission/completion
  policy, recording, hooks and compaction without constructing the TUI.
- Supports text/file tools, foreground/background Bash and subagents,
  TaskOutput/TaskStop, skills/plugins, native/client MCP, load/replay and idle-only
  mode/model changes. Negotiated forms support questions and foreground Unix
  shell input, not secrets. Non-text input remains unsupported.
- See [host capabilities and limits](../../docs/packages/app.md#acp-stdio-host).
  Independent client/editor validation remains outstanding.

## Runtime retention and bounded writes (PR #725)

Rebased onto merged PR #723. ACP keeps one native runtime per session; normal
prompt cancellation preserves the permission gate for the next prompt, including
its tool approval. Terminal turns acknowledge and join native finalization before
returning. Session shutdown resets history, joins hooks and closes recording.

The pinned SDK includes merged sdk-go#102. San opts into a 30-second budget per
active frame write, excluding queue wait. This is not a prompt/cancellation
deadline: a stalled write closes the entire connection and all sessions. Tests
use a shorter budget through the production setup and teardown paths, covering
blocked updates, permission requests, final responses, native finalization and
session cleanup. No detached writes, optimistic delivery or module replace.
The Unix CLI registers nonblocking duplicated stdio with Go's poller and checks
pollability; a real full-pipe regression verifies timeout joins both read/write.
Non-Unix ACP stdio is explicitly unavailable pending an interruptible adapter.

Reproduce the paired native benchmark (no provider/network latency):

```sh
go test ./internal/app -run '^$' -bench BenchmarkACPPrompt -benchmem -benchtime=100x -count=3
```

Go 1.26.9, darwin/arm64, Apple M5, default GOMAXPROCS=10. Median results:

| History messages | Restart ms/op | Reuse ms/op | Restart B/op | Reuse B/op | Restart allocs/op | Reuse allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | 14.010 | 18.131 | 693255 | 508621 | 10611 | 9465 |
| 100 | 18.945 | 18.446 | 864574 | 716388 | 11035 | 10283 |
| 1000 | 19.928 | 19.835 | 2961331 | 2609206 | 18253 | 17503 |

Restart is an in-process reproduction of the former stop/start boundary, not a
separate checkout measurement. Fixed history setup is excluded; protocol pipes,
native model preparation, recording and lifecycle are included. Reuse reduces
allocations but does **not** establish an overall latency win (empty history is
slower). These are allocation counts, not a heap-retention profile. Session-close
tests verify runtime/history release; comprehensive long-lived heap profiling is
still open.

Editor and live vendor acceptance remain untested. Simulated-provider and pipe
tests are not external-client acceptance. No apps, extensions or credentials
were installed/configured. Keep this work Draft pending independent review.
