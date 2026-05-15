# Tracing

How `gencode` records every input that reaches the model, every decision the
harness makes around that input, and every response that comes back — into a
single append-only log that doubles as the session's source of truth, a debug
trace, and a replayable history.

This doc is a first-principles description of what belongs in the trace, the
naming convention each record follows, and how it gets surfaced through
`gen trace`. Storage layout and resume mechanics are covered separately in
[transcriptstore.md](transcriptstore.md); this doc focuses on the *event
model* and the *viewer*.

## First principles

> **Given only the JSONL file, we must be able to reconstruct — byte for byte
> — what the model saw, what it produced, and why the inputs were what they
> were, at every turn.**

That single requirement implies three rules:

1. **Capture causes, not effects.** When something mutates an input
   (system-prompt section, tool registry, model selection, message stream),
   record the mutation. Don't capture only the post-mutation snapshot — that
   loses the chain of "who changed what, in what order."
2. **Record before commit.** The event that changed inputs must be appended
   *before* the `inference.requested` that consumed those inputs. Order in
   the file equals causal order.
3. **Snapshots are derived.** Any "current state" view (current system
   prompt, current tool list, current message chain) is reconstructed by
   replaying records. Snapshots in the file exist only as integrity checks,
   not as primary state.

A trace that follows these rules is also a usable log: every meaningful event
in the system shows up exactly once, in causal order, with full payload.

## Invariants

- **Append-only.** Records never mutate after being written. Compaction and
  fork are themselves recorded as events, not in-place rewrites.
- **One file per session.** `~/.gen/projects/<encoded-cwd>/transcripts/<sessionId>.jsonl`.
  Subagent sessions are separate files linked by `parentId`.
- **Self-contained.** Replaying the JSONL reconstructs everything the agent
  ran with, with one exception: external mutable state (files on disk, time)
  is not recorded — only the fact that a tool read it is.
- **Stable record IDs.** Every record has an `id` derived from `(sessionId,
  monotonic counter or messageId)` so consumers can dedupe and reference
  records across forks.

## Naming convention

Every record type follows one rule:

```
<entity>[.<sub-entity>].<past-tense-verb>
```

- **Lowercase, dot-separated, at most three segments.**
- **Entity is a singular noun** referring to the thing the event is about
  (`session`, `message`, `tool`, `hook`, …).
- **Verb is past tense.** Records are immutable facts about events that
  already happened: `started`, `added`, `removed`, `changed`, `appended`,
  `invoked`, `completed`, `fired`, `decided`, `requested`, `responded`.
- **Pairs are symmetric.** Start/end uses one consistent pair throughout:
  `requested`/`responded`, `invoked`/`completed`, `started`/`ended`.
- **Plural entity ⇒ batch event.** `tools.added` carries an array because
  tools register in batches; `system.section.added` is singular because
  sections mutate one at a time.

The same rule applies to envelope fields and payload keys: lowercase,
camelCase JSON, no abbreviations except universally accepted ones
(`id`, `cwd`).

## Record envelope

Every line in the JSONL shares the same outer shape:

```jsonc
{
  "id":          "<sessionId>:<recordKey>",  // stable, dedupable
  "sessionId":   "<sessionId>",
  "time":        "2026-05-14T22:00:00.123Z", // RFC3339Nano, UTC
  "type":        "<see taxonomy below>",
  "parentId":    "<id of causally preceding record>",  // optional
  "agentId":     "<subagent id>",            // optional
  "isSidechain": false,
  "cwd":         "/abs/path",                // process cwd at write time
  "gitBranch":   "main",                     // optional
  "version":     "1",                        // bumped only on breaking change

  // Exactly one payload object, keyed by the first segment of `type`.
  // E.g. type=session.started → "session": {...}
  //      type=system.section.added → "system": {...}
  //      type=message.appended → "message": {...}
  "<group>": { ... }
}
```

Payload field name **always equals the first segment of `type`**. No
exceptions, no compound payload keys, no `lifecycle`/`elide` aliases. This
is what "elegant" means here: one rule, no special cases.

## Event taxonomy

Eighteen record types, six groups. Past tense throughout.

| Group | Type | Purpose |
|---|---|---|
| Lifecycle | `session.started` | Session opened; initial provider, model, cwd, entrypoint. |
| Lifecycle | `session.forked` | Forked from another session at a given message. |
| Lifecycle | `session.compacted` | Compaction boundary inserted; older messages outside the active chain. |
| Inputs | `system.section.added` | Named section was inserted or replaced in the system prompt. |
| Inputs | `system.section.removed` | Section was removed. |
| Inputs | `tools.added` | One or more tools became available. |
| Inputs | `tools.removed` | Tools were unregistered. |
| Inputs | `model.changed` | Provider/model swapped mid-session. |
| Inputs | `params.changed` | Inference params (max_tokens, temperature, top_p) changed. |
| Conversation | `message.appended` | A user/assistant/tool message entered the active chain. |
| Conversation | `message.elided` | Messages were dropped/truncated before being sent. |
| Tooling | `tool.invoked` | Harness started executing a tool the model requested. |
| Tooling | `tool.completed` | Tool finished; durationMs, success, error. |
| Harness | `hook.fired` | A hook ran; event, cmd, exitCode, head of stdout/stderr. |
| Harness | `permission.decided` | A permission prompt was resolved. |
| Harness | `permission.mode.changed` | Permission mode changed (auto/plan/denyAll). |
| Integrity | `inference.requested` | Wraps a single LLM call with digests of system/tools/messages. |
| Integrity | `inference.responded` | Counterpart with usage, stop_reason, latency, requestId. |
| State | `state.patched` | Projected UI state: title, lastPrompt, tag, mode, tasks, worktree. |

Today's transcript only writes five of these (`session.started` —
historically named `transcript.started` —, `message.appended`,
`state.patched`, `session.compacted`, `session.forked`). The rename and the
remaining thirteen are the surface area added by this design.

## Payload shapes

Each subsection shows the payload object that sits under the group key.

### Session

```jsonc
// session.started
"session": {
  "provider":   "anthropic",
  "model":      "claude-opus-4-7",
  "parentId":   "<source session id if forked or subagent>",  // optional
  "entrypoint": "tui" | "print" | "subagent"
}

// session.forked
"session": { "sourceSessionId": "<id>", "atMessageId": "<id>" }

// session.compacted
"session": {
  "boundaryId":       "<messageId at boundary>",
  "summaryMessageId": "<id of the assistant message holding the summary>",
  "droppedCount":     42
}
```

### System sections

```jsonc
// system.section.added
"system": {
  "name":    "identity",
  "slot":    0,
  "content": "You are Gen Code...",
  "caller":  "command:/identity",
  "digest":  "sha256:..."
}

// system.section.removed
"system": { "name": "identity", "caller": "subagent:exit" }
```

Replay rule: maintain `map[name] -> {slot, content, firstInsertedAt}`;
render-order is `(slot asc, firstInsertedAt asc)`, joined by `\n\n`.

### Tools

```jsonc
// tools.added
"tools": {
  "added": [
    { "name": "Read",  "description": "...", "inputSchema": {...} },
    { "name": "Write", "description": "...", "inputSchema": {...} }
  ],
  "caller": "agent:init"
}

// tools.removed
"tools": { "removed": ["Read", "Write"], "caller": "mode:plan" }
```

### Model & params

```jsonc
// model.changed
"model": { "provider": "anthropic", "model": "claude-sonnet-4-6", "caller": "command:/model" }

// params.changed
"params": { "maxTokens": 16384, "temperature": 0.0, "topP": 1.0, "caller": "config" }
```

### Message

```jsonc
// message.appended
"message": {
  "messageId": "...",
  "parentId":  "<previous message id, or empty>",
  "role":      "user" | "assistant" | "tool",
  "content":   [ ContentBlock, ... ]
}

// message.elided
"message": {
  "reason":      "tokenBudget" | "compact" | "toolResultTruncated",
  "messageIds":  ["...", "..."],
  "bytesDropped": 184320
}
```

`ContentBlock` mirrors the Anthropic shape (text, thinking, tool_use,
tool_result, image) and adds a **`source`** field for provenance:

```jsonc
{
  "type":   "text",
  "text":   "...",
  "source": "user"
            | "hook:UserPromptSubmit:reminders"
            | "command:/identity"
            | "reminder:system-reminder"
            | "compact:summary"
            | "toolTruncated"
}
```

Without `source`, the trace cannot answer "was that paragraph user-typed or
hook-injected?". With it, every byte the model saw is attributable.

### Tool execution

```jsonc
// tool.invoked
"tool": {
  "toolCallId": "toolu_01...",
  "name":       "Read",
  "input":      { "filePath": "/foo.go" },
  "permission": "auto" | "asked" | "denied"
}

// tool.completed
"tool": {
  "toolCallId": "toolu_01...",
  "durationMs": 12,
  "success":    true,
  "bytes":      4096,
  "error":      ""
}
```

The tool *result* rides on the next `message.appended` (role=tool) so the
content stream stays linear. `tool.invoked`/`tool.completed` are pure
telemetry around it.

### Hook & permission

```jsonc
// hook.fired
"hook": {
  "event":      "PostToolUse",
  "name":       "<hook name from settings>",
  "cmd":        "<command line>",
  "exitCode":   0,
  "stdoutHead": "first 1KB...",
  "stderrHead": "first 1KB...",
  "durationMs": 23
}

// permission.decided
"permission": {
  "tool":      "Bash",
  "input":     "{...}",
  "decision":  "allow" | "deny",
  "scope":     "once" | "session" | "always",
  "by":        "user" | "autoRule" | "config",
  "rationale": "matched allow-rule: Bash(git status)"
}

// permission.mode.changed
"permission": { "mode": "auto" | "plan" | "denyAll", "caller": "command:/permission" }
```

### Inference

`inference.requested` and `inference.responded` are the only records that
hold *snapshots* — and they hold digests, not full payloads, because the
full payloads are reconstructible from the events that preceded them.

```jsonc
// inference.requested
"inference": {
  "turn":         7,
  "agentTurn":    "main-007",                // or "main-005:explore-002"
  "provider":     "anthropic",
  "model":        "claude-opus-4-7",
  "maxTokens":    16384,
  "temperature":  0.0,
  "systemDigest": "sha256:...",
  "toolsDigest":  "sha256:...",
  "messageIds":   ["m1", "m3", "m7"]
}

// inference.responded
"inference": {
  "turn":       7,
  "stopReason": "tool_use",
  "latencyMs":  1820,
  "requestId":  "req_011...",
  "usage": {
    "inputTokens":              4321,
    "cacheReadInputTokens":     1024,
    "cacheCreationInputTokens": 256,
    "outputTokens":             512
  }
}
```

If `systemDigest` doesn't match the digest reconstructed by replaying
preceding `system.section.*` records, the trace has an integrity bug — that's
the signal something was sent to the model without being recorded. Same for
`toolsDigest`.

## Replay

To reconstruct exact inference inputs at record N:

```
state = {
  sections: map[name]{slot, content, firstInsertedAt},
  tools:    ordered list,
  model:    {provider, model},
  params:   {maxTokens, temperature, topP},
  messages: ordered list,
}

for rec in records[0..N]:
  switch rec.type:
    case "system.section.added":    state.sections[rec.system.name] = ...
    case "system.section.removed":  delete state.sections[rec.system.name]
    case "tools.added":              state.tools += rec.tools.added
    case "tools.removed":            state.tools -= rec.tools.removed
    case "model.changed":            state.model = rec.model
    case "params.changed":           state.params = rec.params
    case "message.appended":         state.messages += rec.message
    case "message.elided":           state.messages -= rec.message.messageIds
    case "session.compacted":        state.messages = chainFrom(rec.session.boundaryId)
    case "inference.requested":
      assert digest(render(state.sections))     == rec.inference.systemDigest
      assert digest(canonicalize(state.tools))  == rec.inference.toolsDigest
      assert state.messages.ids()               == rec.inference.messageIds
```

Render order for sections: sort by `(slot asc, firstInsertedAt asc)`, join
non-empty renders with `\n\n`. Mirrors `core/system_impl.go:build`.

## Consumers

The trace is meant to serve three uses without modification:

1. **Session resume** — what `transcriptstore` already does. New record
   types project as no-ops on the legacy reader; the rename of
   `transcript.*` → `session.*` is the only forward-incompatible change and
   is handled by the migration note below.
2. **`gen trace`** — the web viewer described in the next section.
3. **Log / grep target** — every event has a stable `type` and structured
   payload, so `jq` over the JSONL is enough for ad-hoc analysis.

### Troubleshooting recipes

**Why was section X in the system prompt at turn N?**

```bash
jq 'select(.type|startswith("system.section."))' <session>.jsonl \
  | jq 'select(.system.name=="identity")'
```

Walks every mutation of `identity` with its `caller`.

**Why did the model not see tool X at turn N?**

```bash
jq 'select(.type=="tools.added" or .type=="tools.removed")' <session>.jsonl
```

Cross-reference against the turn's `inference.requested.toolsDigest`.

**Which hook injected this text into my user message?**

Find the `message.appended` for that message, locate the `ContentBlock`
containing the text, read its `source` field.

**Why did this tool call run without asking?**

```bash
jq 'select(.type=="permission.decided")' <session>.jsonl
```

The matching record has `by` and `rationale` fields.

**What changed between turn N-1 and N?**

Diff the two adjacent `inference.requested` digests. Any difference points
to a recorded mutation between them.

## `gen trace` — the web viewer

A local web app that renders the JSONL into an interactive timeline, with
live updates while a session runs. Zero-config: random port, localhost-only,
single binary (assets embedded).

### Command surface

```text
gen trace                          # start viewer, open browser, list sessions in current project
gen trace --port 38080             # pin port; default is auto-allocated
gen trace --no-open                # print URL only, don't shell out to open the browser
gen trace --all                    # include sessions from other project dirs
gen trace export <id> -o out.html  # write a self-contained, offline HTML for one session
gen --trace                        # run a session as normal AND start the viewer in-process
```

`gen --trace` is the live mode: when launching a session it boots the viewer
on a random localhost port, prints the URL, and shuts the server down on
session exit. The viewer auto-selects the currently running session and
follows it in real time.

### Architecture

```
┌──────────────────┐
│  browser (SPA)   │
│  - timeline      │   HTTP + SSE
│  - detail panel  │ ◀─────────────┐
│  - replay state  │               │
└──────────────────┘               │
                                   │
                          ┌────────┴─────────┐
                          │ gen trace server │
                          │ - net/http       │
                          │ - fsnotify       │
                          │ - embed.FS UI    │
                          └────────┬─────────┘
                                   │ read-only
                          ┌────────┴─────────┐
                          │ ~/.gen/projects/ │
                          │ <cwd>/transcripts│
                          └──────────────────┘
```

### HTTP surface

| Method | Path | Returns |
|---|---|---|
| GET | `/` | SPA shell (single HTML file from `embed.FS`) |
| GET | `/api/sessions` | List `[{id, title, model, messageCount, createdAt, updatedAt, branch, parentId}]` |
| GET | `/api/sessions/{id}/records?after=<id>&limit=K` | Paginated records; raw JSONL records, no reshaping |
| GET | `/api/sessions/{id}/stream` | SSE stream; emits each new record as it's appended |
| GET | `/api/sessions/{id}/state/{recordId}` | Replay result: reconstructed `{sections, tools, model, params, messages}` at that point |
| GET | `/api/sessions/{id}/integrity` | Runs digest checks; returns `{ok, mismatches:[{recordId, expected, got}]}` |

The records endpoint returns the bytes of the JSONL untouched (no
re-serialization). The wire format **is** the file format. This collapses
two schemas into one and means a browser cache of records is byte-identical
to the file on disk.

### Live updates

Backend watches `~/.gen/projects/<encoded-cwd>/transcripts/` with
`fsnotify`:

- File size grew → read new lines, parse, fan out to every SSE subscriber
  for that session.
- New `.jsonl` appeared → push a "session appeared" event to the
  all-sessions SSE channel.
- File renamed/removed → push a "session closed" event.

SSE is preferred over WebSocket: one-way push is all that's needed, plain
`EventSource` works, no framing layer.

### UI

```
┌──────────────────┬──────────────────────────────────────┐
│  Sessions        │  ▶ session 5319a6e1   live ●         │
│  ───────         │  ─────────────────────────────────── │
│  ● running       │  Timeline                Detail      │
│    5319a6e1     │  ───────                 ──────       │
│  ○ resumed      │  T1 session.started ⚑    role: user   │
│    184d4011     │  T2 system.section ⚙ →   text:        │
│  ○ forked       │  T3 system.section ⚙ →   "explain..." │
│    77386800     │  T4 tools.added 🔧 →     source: user │
│                  │  T5 message ← user 💬                  │
│  [+ all projects]│  T6 inference ▶                       │
│                  │  T7 message ← assist 💬                │
│  Filters:        │  T8 tool.invoked 🛠                    │
│  ☑ message       │  T9 tool.completed ✓                  │
│  ☑ system        │  T10 message ← tool                   │
│  ☑ inference     │  T11 inference ◀                      │
│  ☑ tool          │  T12 hook.fired 🪝                    │
│  ☑ hook          │  T13 permission 🛡                    │
│  ☐ state.patched │  ▶ live tail                          │
└──────────────────┴──────────────────────────────────────┘
```

UI principles:

- **No build step.** Vanilla JS or htmx + light JS, served from `embed.FS`.
  No bundler, no transpile. A future redesign can swap in a framework
  without touching the server.
- **Group-colored rows.** Each record group has one color/icon; record type
  shown as a small label. Quick visual scan = quick triage.
- **Filters per group.** Default filter hides `state.patched` (UI noise).
  User toggles to surface anything.
- **"Show only changed system" mode.** Collapses the timeline to just
  `system.section.*` records — instant prompt-evolution view.
- **Click row → full payload + state snapshot.** Detail panel shows the raw
  record JSON and the replayed state at that point (so you can see "what
  the model saw at turn 7" without leaving the page).
- **Diff mode.** Pick two `inference.requested` records, get a side-by-side
  diff of system prompt, tools, and message tail.
- **Live badge.** When SSE is connected and the session is being written
  to, show a "live ●" indicator and auto-scroll. Pause auto-scroll on user
  scroll-up; resume on jump-to-bottom.

### `gen trace export`

Produces a single HTML file with the JSONL inlined as a JS literal plus the
SPA assets. Open it in any browser, no server needed. Use for:

- attaching to bug reports
- archiving a debugging session
- sharing with someone who doesn't have `gencode` installed

Export is **read-only** and **offline**: no fetches, no SSE — just the
static snapshot.

### Security & operational notes

- **Bind 127.0.0.1 only.** Never 0.0.0.0. The trace contains everything the
  model saw, including any secrets the user pasted.
- **No auth.** Localhost trusts the OS user. Same posture as `gen` itself.
- **Read-only API.** No endpoint mutates the JSONL.
- **Process lifecycle.**
  - `gen trace` runs until Ctrl-C.
  - `gen --trace` runs the server for the lifetime of the session it
    started, shuts it down on session exit, prints the URL once on start.
- **Port discovery.** On `gen --trace`, write the chosen port to
  `~/.gen/projects/<cwd>/.trace-port` so external tooling (editors,
  bookmarklets) can pick it up. Delete on exit.

### Why this design

- **SSE over WebSocket.** Push-only fits the data flow; no framing protocol
  needed.
- **JSONL as the wire format.** One schema, not two. The client renders
  the same shape that's on disk.
- **fsnotify, not polling.** Live updates are immediate; idle viewer costs
  nothing.
- **Embedded assets.** Single binary, no asset directory to install.
- **Localhost + random port.** No firewall surface, no auth complexity.

## Implementation gap

Listed in dependency order.

### 1. Schema additions & rename

- `internal/session/transcript/records.go`:
  - Add new `RecordType` constants for all eighteen types.
  - Add typed payload structs (`SessionRecord`, `SystemRecord` is reused,
    `ToolsRecord`, `ModelRecord`, `ParamsRecord`, `ToolRecord`,
    `HookRecord`, `PermissionRecord`, `InferenceRecord`,
    `ElideRecord`-as-MessageRecord-variant, etc.). Wire them onto `Record`.
  - Rename `Record.TranscriptID` JSON tag `transcriptId` → `sessionId`.
- `internal/session/transcript/projector.go`:
  - Accept both old (`transcript.started/forked/compacted`) and new
    (`session.*`) type strings during a deprecation window. New writes use
    `session.*`.
  - `applyStatePatch` falls through unknown patch paths with
    `default: continue`.
  - Project new record types onto a richer `Transcript` view
    (`Transcript.SectionsTimeline`, `Transcript.ToolsTimeline`, etc.).

### 2. ContentBlock provenance

- `internal/session/transcript/records.go`: add `Source string` to
  `ContentBlock`. Default empty (treated as `"user"`).
- Producers that inject content tag it at write time:
  - `internal/reminder/` → `reminder:<name>`
  - `internal/hook/` UserPromptSubmit / SessionStart additionalContext
    paths → `hook:<event>:<name>`
  - `internal/command/` slash command expansion → `command:<name>`
  - compact summary insertion → `compact:summary`
  - tool result truncation marker → `toolTruncated`

### 3. Event bus reuse

The agent already has an event outbox (`internal/core/agent_impl.go:465`).
Extend `core.Event` with new variants for `SystemSectionChanged`,
`ToolsChanged`, `ModelChanged`, `ParamsChanged`, `ToolInvoked`,
`ToolCompleted`. The existing session subscriber translates them into
transcript records — no new `Recorder` interface threaded across packages.

Subsystems outside `core.Agent` (hooks, permission, slash commands) get a
lightweight `EventSink func(Event)` injected from
`internal/app/services.go`. Same sink, different entry points.

### 4. System / tools instrumentation

- `internal/core/system_impl.go`: in `Use`/`Drop`, after mutating
  `s.sections`, emit `SystemSectionChanged{name, slot, content, caller}`.
  `caller` is plumbed via an explicit parameter on `Use`/`Drop` (more
  honest than a hidden context value).
- `internal/core/tools/...`: emit `ToolsChanged` on Register/Unregister.
- `internal/core/agent_impl.go:streamInfer`: after rendering, compute
  digests and emit `InferenceRequested`. After the stream finishes, emit
  `InferenceResponded`.

### 5. Tool / hook / permission instrumentation

- Wrap tool execution with `ToolInvoked` / `ToolCompleted` at the call site.
- `internal/hook/`: emit `HookFired` after each hook returns, with head of
  stdout/stderr capped at 1KB.
- Permission bridge: emit `PermissionDecided` when a request resolves;
  emit `PermissionModeChanged` on mode swap.

### 6. `gen trace` package

- New `internal/trace/` package: HTTP server, fsnotify watcher, SSE
  fan-out, integrity replay.
- New `internal/trace/ui/`: static SPA assets, embedded via `embed.FS`.
- New `cmd/gen` subcommand `trace` with subcommands `serve` (default),
  `export`. Top-level `--trace` flag wires the server into session
  startup.

### 7. Retire the parallel `DEV_DIR` path

Once `inference.requested`/`inference.responded` are written into the
transcript with digests, `internal/log/devdir.go` per-turn JSON files
become redundant. Remove them in a follow-up; `GEN_DEBUG` still controls
human-readable log lines, but no longer writes a second copy of the
inference payload.

### 8. Schema versioning

Add `version: "1"` to every new record. Bump only on incompatible change
(field meaning changes, required record removed). Adding new record types
is backward-compatible and does not bump the version.

## Open questions

- **Section caller plumbing.** Threading `caller` through every
  `system.Use/Drop` is noisy. Acceptable to leave it empty for paths that
  don't know, or do we hard-require?
- **Tool input redaction.** `tool.invoked.input` may contain secrets if a
  user pastes credentials into a Bash command. Same risk as today's
  `message.appended` but worth a redaction policy decision before this
  ships — particularly because `gen trace export` produces a shareable
  artifact.
- **Digest canonicalization.** Tool schemas come from third-party MCP
  servers and may not be deterministic JSON. Decide once: canonical JSON
  (sorted keys, no whitespace) before hashing.
- **Multi-writer safety.** Today's `FileStore` uses a per-process mutex.
  Two concurrent `gen` invocations on the same session would interleave
  records. Out of scope for this doc, but the trace model assumes one
  writer at a time. The `gen trace` viewer is a reader and unaffected.
