# Inspector

The **Inspector** is a local web UI for exploring and debugging gen code
session transcripts. It reads the JSONL transcript files stored under
`~/.gen/projects/` and presents every event — user messages, model
responses, tool calls, system events, and harness state — as an
inspectable timeline. Think of it as "conversation forensics" for your
agent runs.

![Inspector UI](images/inspector.png)

## Quick Start

```bash
gen inspector
```

This starts a localhost-only web server on a random port and opens the
inspector in your default browser. Press `Ctrl-C` to stop.

```bash
# Pin a specific port
gen inspector --addr 127.0.0.1:38080

# Print the URL but don't open the browser
gen inspector --no-open
```

> **Security note:** The inspector binds to loopback addresses only
> (`127.0.0.1`, `::1`, `localhost`). Transcripts contain everything the
> model saw, including secrets in tool inputs — binding to a non-loopback
> address is rejected.

## What You Can Do

### Browse Sessions

The left sidebar lists all past sessions for the current project
directory. Click any session to load its transcript.

### Navigate the Timeline

The center panel displays every record in the transcript as a
chronological timeline. Each record is tagged with its event type:

- **User messages** — text the user typed or piped in
- **Model responses** — text and thinking output from the LLM
- **Tool calls** — function calls the model made (arguments and results)
- **System events** — session start, compaction, errors
- **Harness state** — system prompt sections, tool schemas, permission
  decisions

Filter chips at the top let you show or hide specific event types so you
can focus on what matters.

### Inspect Raw Records

Click any record to see its raw JSON in the detail pane (right panel).
This is the exact JSONL line as written to disk — no transformation, no
hidden fields.

### Examine the System Prompt

At any record, you can open the **System Prompt overlay** to see the
full system prompt that was active at that moment. This includes all
sections injected by the harness: personas, skills, project memory
(`GEN.md` / `CLAUDE.md`), tool schemas, and MCP server prompts.

### Replay State

The **State** tab reconstructs the model's complete context at any point
in the transcript:
- **System prompt** — full text with all injected sections
- **Tool schemas** — JSON Schema definitions for every tool available
- **Active message chain** — the exact messages in context (respecting
  compaction boundaries and system-reminder injection)

### Integrity Checking

At each inference request (`inference.requested`), the inspector
recomputes digests of the replayed state and compares them against what
was recorded:
- **System prompt digest** — SHA-256 of the assembled system prompt
- **Tools digest** — SHA-256 of the serialized tool schemas
- **Message IDs** — ordered list of message identifiers

Mismatches are flagged with a **BAD** badge, showing exactly which
messages are missing or extra. This is useful for debugging compaction
bugs, harness injection issues, or unexpected state drift.

### Live Tail

When you have an active gen code session running in another terminal,
the inspector can live-tail new records via Server-Sent Events (SSE).
Open the session and toggle the **Live** switch — new records appear in
the timeline as they are written to disk.

## UI Layout

| Area | What It Shows |
|------|---------------|
| **Sidebar** (left) | Session list: project sessions sorted by date |
| **Timeline** (center) | Chronological record stream with filter chips |
| **Detail** (right) | Raw JSON for the selected record |
| **System Prompt overlay** | Full system prompt at a given point in time |
| **State tab** | Reconstructed context: system + tools + messages |

## How It Works

1. `gen inspector` starts an HTTP server bound to loopback.
2. The server reads transcript JSONL files from
   `~/.gen/projects/<encoded-cwd>/transcripts/`.
3. The embedded SPA (single-page application) fetches session lists and
   records via a REST API, and subscribes to live updates via SSE.
4. The **replay engine** (`replay.go`) walks the event log from the
   beginning to reconstruct the model's full context at any record index.
   Results are cached in an LRU (capacity 64) so timeline scrubbing stays
   fast.
5. All processing is read-only — transcripts on disk are never modified.

## API Endpoints

The inspector's HTTP API is designed for its own UI, but you can call it
directly for scripting:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | The inspector SPA |
| `GET` | `/api/sessions` | List all sessions for the project |
| `GET` | `/api/sessions/{id}/records?after=N` | Transcript records as NDJSON, paginated |
| `GET` | `/api/sessions/{id}/stream` | SSE live-tail of new records |
| `GET` | `/api/sessions/{id}/state/{recordID}` | Replayed state at a given record |

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `127.0.0.1:0` | Bind address (loopback only; random port by default) |
| `--no-open` | `false` | Print the URL without launching the browser |

## See Also

- [Package design doc](packages/inspector.md) — internals and contracts
- [Session transcripts](packages/session.md) — the JSONL format on disk
- [Compaction](concepts/compaction.md) — how context window compaction
  interacts with transcript replay
