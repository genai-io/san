# Inspector

The **Inspector** is a local web UI for exploring san's native session events:
messages, model requests, tool calls, permissions, hooks and harness state. It
reads committed JSONL batches beneath `~/.san/projects/` without acquiring a
session writer or modifying session data.

![Inspector UI](../images/inspector.png)

## Quick Start

```bash
san inspector

# Pin a specific port
san inspector --addr 127.0.0.1:38080

# Print the URL without opening a browser
san inspector --no-open
```

The command starts a loopback-only server on a random port and opens your browser
unless `--no-open` is set. Press `Ctrl-C` to stop.

> **Security:** Session events and request blobs can contain everything a model
> saw, including sensitive tool inputs. Non-loopback bind addresses are rejected.
> The local API has no authentication: treat the port as trusted, avoid shared
> multi-user hosts and stop the server when finished.

## Browse and inspect

The sidebar lists live sessions of the canonical project, ten per page. Use
**Previous** and **Next** to browse, or **Refresh** to see newly created sessions.
Git worktrees share one project; unrelated clones and non-Git directories do not.
Renamed titles take precedence over generated titles. If a title would require
replaying a large log, the sidebar shows a shortened session ID; hover over the
row to see the full ID. Unsupported sessions remain available for diagnosis.

The timeline exposes thirteen event families: session creation/update, native
messages, context compaction, inference start/finish, permission decisions, tool
finishes, instruction/toolset/skill catalogues, hook finishes and annotations.
Use the filters under **Timeline** to focus on related events. Rows show a short
name, a preview, and any recorded outcome. Unmapped types display their raw event
names. Full hook commands and system reminders remain in the details. Sequence
positions are stable within each session and are not JSONL line numbers: one
committed line may contain multiple events.

Select a session on the left and a step in **Timeline** to see its recorded details
on the right. Use **Expand** to give the details more room without changing the
selected step; **Restore** or `Esc` returns to the original layout. Message steps
show the ordered native blocks. Permission and tool steps link to the requested invocation; tool
finishes show any recorded file diff, shell output or agent result separately
from what was returned to the model. Source links jump to the referenced event.

The details pane opens with **Model input** for a model call or **Context** for
other steps. Context shows session state after the selected step, not a recorded
model request. **Event** and **Raw batch** are collapsed until opened. The latter
is the only raw JSON view and may include several events. System reminders are
shown separately inside messages. Transport failures report checked batches and
bounded unvalidated text.

## Reconstruct the model request

At `inference.start`, the inspector asks the session backend for the request that
was admitted at that exact historical prefix. A conversation request reconstructs
instructions, tool schemas and repaired active native messages; a standalone
request loads its immutable, verified blob. Current settings and later catalogue
changes do not substitute for the historical input.

For other events, historical context includes changes made by the selected step.
The separate **Session history** view shows all committed messages through the
indicated step, including turns before compaction. Native images, provider
signatures, encrypted reasoning and content ordering are retained. Unsupported
event versions or invalid provenance fail validated replay instead of being
silently skipped.

## Outcomes and interrupted work

`inference.finish` links its start and carries outcome, usage, duration and any
structured error. Each retry has a distinct start/finish; an unfinished start is
an interrupted call, not a fabricated failure or response.

A `permission.decide` explains the actual authority and, where shown, the human
question snapshot. `tool.finish` records an execution outcome, raw display details
and child-session correlation. Missing finishes do not prove failure: an allowed
call without a committed finish has an unknown outcome. Inspection never reruns
models, hooks or tools.

## Live Tail

Opening a running session subscribes to SSE. Only complete committed batches
appear. Partial suffixes wait for completion; corruption or replacement of the
previously checked prefix ends the stream with a diagnostic rather than splicing
unrelated histories. Browser request generations fence stale selections and
reconnection deduplicates stable identities.

## Storage and API

The server reads:

```text
~/.san/projects/<project>/sessions/<session-id>/events.jsonl
```

There is no older-format reader or fallback to `transcripts/`; sessions written
by earlier versions remain on disk untouched and are not discovered.

| Method | Path | Description |
|---|---|---|
| `GET` | `/` | Embedded inspector SPA |
| `GET` | `/api/sessions` | Page of live sessions (`items` plus optional `next` cursor) |
| `GET` | `/api/sessions/{id}/records` | JSON with committed SDK batches, tails and any diagnostics |
| `GET` | `/api/sessions/{id}/stream` | SSE of committed batches |
| `GET` | `/api/sessions/{id}/state/{seq}` | Validated native context or historical inference request |

Listing, state reconstruction and streaming do not modify logs, attrs, locks or
cache files. Server shutdown drains requests; each stream stops with its request.

## See Also

- [Inspector package](../packages/inspector.md) — contracts, tests and internals
- [Sessions](../packages/session.md) — events, request blobs and lifecycle
- [Compaction](../concepts/compaction.md) — active context versus full history
