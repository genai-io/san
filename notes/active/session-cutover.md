# Session cutover plan

Moves the runtime from schema-4 transcripts to the typed `sessions/<sid>/` store
([PROP-0005](../../docs/design/proposal-0005-session-management.md)). The storage
layer is done and unwired (events, projector, blobs, attrs, overview cache, trash,
list, fork, clean). Name isolation and the public typed API are implemented and
unwired; the producers, runtime resume and final switch remain.

## Why a flag day

A session has one format. The runtime either writes typed events or schema-4
records, so the switch cannot be done producer by producer. The plan is to make
every piece ready and tested while unwired, then switch once and delete the old code.

## Steps

1. **Free the names (done).** Schema-4 contracts and callers use `Legacy*`;
   typed `Store` / `Session` own `store.go` / `session.go` without a format switch.
2. **Public typed API (done; unwired).** `Store` (`Create`, `Open`, `Fork`, `Get`,
   `List`, `Events`, `SetAttrs`, `Delete`, `Restore`, `Clean`) and `Session` (`ID`,
   `Path`, `Err`, `Append`, `Snapshot`, `PutRequest`, `Request`, `Close`) wrap the
   typed storage. Tests cover atomic batches, concurrent appends, provenance,
   standalone requests, lifecycle and scoped cleanup. Runtime still uses `Legacy*`.
3. **Recorder (core adapters done; unwired).** `recorder.go` maps runtime
   admissions and observations; `inference.go` records/rebuilds every purpose's
   effective request and atomically commits results with finishes. Real-agent
   golden sequences cover the turn/tool path, denied calls and write failures.
   sdk-go [#109](https://github.com/genai-io/sdk-go/pull/109) supplies native tool
   choice JSON and reliable tool execution evidence. Production utility-call,
   resolved permission and prompt-hook handoffs still need integration:

   | Runtime source | Typed events |
   | --- | --- |
   | `MessageAdded` / user input | `message.append` |
   | `InferenceStart` / `InferenceEnd` | `inference.start` (purpose, input kind, digests) / `finish` |
   | permission gate | `permission.decide` (+ `asked`) before the tool runs |
   | `ToolEnd` | `tool.finish` (+ `child_session`) |
   | `SystemChange`, `ToolsChange`, skill state | `instructions` / `toolset` / `skills.update` with `cause` |
   | `Compacted` | `context.compact` after `inference.start {compaction}` |
   | hook audits | `hook.finish` |
   | title, todos, cwd, model, mode, group hint | `session.update` |
   | notices | `annotation.add` |

   Non-turn model calls (judge, hook, autopilot, group, compaction) record a
   `standalone` request copy in `blobs/`; `btw`, `recap`, `suggest` record
   `conversation` calls with `append`.
4. **Resume.** `Open` → projection → current settings and trust → runtime; report
   `Unfinished()`; ACP checks the requested cwd against `execution_cwd`; catalogues are
   rebuilt from current config and differences written with `cause: resume`.
5. **Subagents.** Child `session.create {parent}` committed before the child runs,
   `tool.finish {child_session}` in the parent, later results as `message.append` with a
   foreign `source` and evidence.
6. **Switch and delete.** Point `internal/app`, `agent`, `subagent`, hooks, ACP and the
   inspector at the new API; delete `Legacy*`, schema-4 records and projector,
   `internal/transcript`, and the legacy-partition code. Move the hook-facing
   transcript path to `events.jsonl`. Re-apply the inspector UI from
   `feat/session-typed-read-integration` on typed events.

## Open items

- Event pages by offset need `Store.BatchesFrom` in sdk-go; until then each page reads the prefix.
- Classify the model call in `internal/agent/memory_recall.go` before utility integration.
- Retention age is not a setting yet; `Clean` takes it as an option.
- Decide at step 6 whether `internal/session` keeps `Store`/`Session` or the typed
  pieces move to a leaner package; the rename in step 1 keeps either possible.
