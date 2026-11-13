# Session cutover plan

Moves the runtime from schema-4 transcripts to the typed `sessions/<sid>/` store
([PROP-0005](../../docs/design/proposal-0005-session-management.md)). The storage
layer is done and unwired (events, projector, blobs, attrs, overview cache, trash,
list, fork, clean). Public APIs, producer adapters, native resume and child
execution are implemented but not default-enabled. The remaining PR switches all
hosts and the inspector together, then removes legacy persistence.

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
   choice JSON and reliable tool execution evidence. Initial producer adapters
   are implemented and tested, still unwired:
   - SDK [#110](https://github.com/genai-io/sdk-go/pull/110) supplies native
     `CompleteWithAdmission`; the LLM layer adds per-attempt admission/finishing without
     reimplementing preparation, repair or aggregation.
   - Final permission recording keeps explicit authority/scope and detached
     human question snapshots, failing closed before dispatch.
   - Hook finishing joins interpreted outcomes with inference accounting; queued
     and detached invocations retain the original owner. Recording errors are fatal
     at prompt/tool/Stop gates, unlike ordinary hook execution errors.
   - BTW, recap and suggestion scopes capture the original session. Recap caches
     publish only at display; discarded or retired work finishes without an
     annotation. Suggestions record accounting, never suggested context.
   - Manual, automatic and subagent compaction adapters retain per-attempt usage
     and atomically join the exact accepted summary with its inference finish.
     Deferred native acceptance precedes context replacement; stale/canceled
     requests and queued shutdown work discard against the original writer.
     Child-writer injection is opt-in in step 5; default wiring waits for step 6.
   - Permission-judge calls join the final gate verdict with inference accounting;
     grants follow a committed allow. Ordinary judge failures still escalate.
   - Autopilot closes parse attempts separately and commits prepared continuation
     input at runtime acceptance; expired and superseded results cannot act.
   - Group interpretation records the actual response, not a planned operation.
     External membership changes are not rolled back on a later recording failure.
   - Bash prompts, mission drafts and questions record accounting without invented
     conversation output. Memory selection uses standalone purpose `recall`.

   Default runtime wiring waits for steps 4–6.

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

   Non-turn model calls (judge, hook, autopilot, group, compaction, recall) record a
   `standalone` request copy in `blobs/`; `btw`, `recap`, `suggest` record
   `conversation` calls with `append`.
4. **Resume (native adapter ready).** `Runtime.Resume` validates the opened owner
   and execution cwd, seeds active context and returns `Unfinished()` without replay.
   Current catalogue additions, changes and removals commit with cause `resume`.
   Hosts resolve settings/trust and present interruptions at step 6.
5. **Subagents (typed execution ready).** Child identity commits before dispatch;
   continuation retains the original parent/tool call. Native admissions record
   child execution and return committed message sources. `SendInput` atomically
   accepts prepared child messages with foreign provenance against the original
   receiver. Default host injection and notification routing switch in step 6.
6. **Switch and delete.** Point `internal/app`, `agent`, `subagent`, hooks, ACP and the
   inspector at the new API; delete `Legacy*`, schema-4 records and projector,
   `internal/transcript`, and the legacy-partition code. Move the hook-facing
   transcript path to `events.jsonl`. Re-apply the inspector UI from
   `feat/session-typed-read-integration` on typed events.

## Open items

- Event pages by offset need `Store.BatchesFrom` in sdk-go; until then each page reads the prefix.
- Retention age is not a setting yet; `Clean` takes it as an option.
- Decide at step 6 whether `internal/session` keeps `Store`/`Session` or the typed
  pieces move to a leaner package; the rename in step 1 keeps either possible.
