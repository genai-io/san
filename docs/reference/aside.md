# Aside

An Aside is a focused task outside the main conversation, usually a single model call. It can use a read-only snapshot of the main context or build its own request. Its working messages do not enter main history. Only the accepted result is returned: a display-only Notice, model-facing context, an editable draft, or a decision for the runtime to validate.

The diagram shows the main delivery paths. Inspector records the requests and responses separately from their accepted results.

```text
Trigger                      Aside                 Result
User asks a question      -> Auto Memory / Recall  -> Note previews in an eligible main request
Completed turn or command -> Auto Memory / Update  -> Validated store change, then display-only Notice
User asks /btw            -> By the Way             -> Display-only Notice
Eligible turn ends        -> AutoPilot              -> Proposed next step; runtime decides
ReviewSkill and a clean turn   -> Auto Skill             -> Validated skill change, then Notice
```

Asides stay in the same session for Inspector. Only accepted model-facing results enter the main context.

| Aside | Trigger | Tools in the request | System prompt and messages | Result |
|---|---|---|---|---|
| **Auto Memory · Recall** | A user message with enabled Recall and new candidates or a truncated index. | None. San reads the repository store locally. | The first request contains the index preview, already-covered IDs, and the question. | Up to five selected note IDs. San attaches note previews to an eligible main request. |
| **By the Way** | Explicit `/btw <question>`. | The active main conversation's tool schemas, including enabled MCP tools, for prefix reuse. The Aside cannot execute them. | Main system prompt and active history, then a separate user message with the side instruction and question. | A text answer shown as a Notice, not sent to the main model. |
| **AutoPilot** | An enabled decision point, such as turn end, an eligible approval review, or a user-question steer. | None. A tool under review is input data, not an available tool. | A decision-specific prompt and one task message with bounded evidence, such as the mission, recent progress, or eligible choices. | A structured decision. The runtime may accept it only under existing permissions and continuation limits. |
| **Auto Skill** | At least one skill action is allowed, and a clean turn ends after a `ReviewSkill` request. | None. | A review prompt, configured strategy, and a bounded excerpt of the settled turn and relevant skills. | San validates at most one change to an agent-owned skill. The change is recorded separately from the main model's history. |
| **Recap** | Explicit `/recap`, or an eligible absence. San can prepare a recap while the terminal is unfocused. | Main tool schemas for reuse; none executed. | Main system prompt and history, then a short recap instruction and relevant status facts. | A brief status and next action shown in the UI. A prepared recap is reused only while it still describes the current conversation. |
| **Suggestion** | When suggestions are enabled and a completed turn leaves enough context; deferred while the terminal is unfocused. | Main tool schemas for reuse; none executed. | Main system prompt and history, then an instruction to suggest the next input; includes the active mission when applicable. | An editable input hint, never submitted automatically. |
| **Flow Draft** | Explicit `/flow <goal>`. | None. | Main system prompt and history, then the drafting instructions, available agents and skills, and the user's goal. | An unsaved draft in Review. Saving and running are separate user actions. |

## Auto Memory lifecycle

Recall and Update share a repository store. Recall has no tools. Update keeps the main tool schemas and order unchanged; that list is not permission to execute every tool. Update reuses native file tools on private copies. Its `Read` accepts only `file_path` and presents the bounded snapshot as raw text (8 KiB per note, 16 KiB total), not the ordinary numbered preview. Only a latest main input of `Forget build.md` (case-insensitive English verb) or `忘掉 build.md`, with the desired filename substituted, authorizes a standalone shell proposal for one previously read, indexed note without another prompt.

| Operation | Trigger | Task message | Accepted result |
|---|---|---|---|
| **Recall** | **Fast** runs alongside the main request; **Wait** waits up to the configured timeout. | Three user messages: raw index preview (plus a separate hint block if truncated), `Already in main context:`, then `User input:`. | Up to five selected previews. Fast adds request-only context at the next eligible request boundary; Wait attaches Reminders to the submitted user message. Failure does not block the turn. |
| **Update** | `/reflect memory` or an eligible turn when automatic Update is on. | The admitted main request and its final assistant reply, followed by one user message: task, layout, optional note example, fenced index preview, and optional truncation hint. | A validated batch of adds or edits, including index edits that preserve note links, or one removal. Results are display-only; earlier commits may survive an incomplete batch. |

Both Asides can read or search omitted index entries on demand. Each lookup appends an assistant JSON reply and a user message with separate status and index data blocks. An Update `Read` round instead appends the assistant tool-call response and a user message with matching tool results. Neither changes the admitted main prefix. See [Auto Memory](../concepts/auto-memory.md#example-aside-requests) for request examples and limits.

Natural-language input is never routed to Update based on its wording. The main agent handles project instructions through ordinary files and permissions. Update runs outside the main turn and does not manage those files. Its working messages, store paths, indexes, and status stay out of the main context. A disabled store starts no new Recall or Update work; settings changes do not rewrite requests already scheduled. Update rechecks the session and store, and automatic runs also recheck the Update switch, before scheduling a write. At commit time, freshness checks are best-effort against external writers; earlier commits are not rolled back if a later step fails. Configure these in `/reflect` under Auto Memory; `/memory` opens the store and instruction-file browser.

## Recap

Open `/reflect` and press `Tab` to switch to **Recap**. Select a row with `↑`/`↓` and press `Enter` to edit or save:

| Setting | Default | Meaning |
|---|---:|---|
| Time away (minutes) | 5 | Minimum time the terminal is unfocused. `-1` turns off automatic recaps. |
| Turns between recaps | 3 | Minimum completed user turns since the last displayed recap (including after resume), or since the start of the session before the first recap. |

Each prompt you submit counts as one turn when the main agent finishes normally. Tool calls, automatic continuations, and failed or canceled turns do not count. Older sessions without recorded turn counts start counting with the next completed user turn.

Meeting the turn threshold doesn't cause a recap while you stay focused. Returning during a running turn skips the automatic recap. Preparing one early can reuse the warm prompt cache, but a result isn't shown before the away threshold. Automatic recaps need terminal focus events (in tmux, `set -g focus-events on`); `/recap` works without those events or any automatic threshold.

The tab saves `recap.afterMinutes` and `recap.turns` in project settings for Git repositories and user settings otherwise. If `recap.afterMinutes` is unset, the old `autoPilot.recapAfterMinutes` settings key is still honored.

## Context and caching

Asides follow the same [Context Management principles](context-management.md#principles) as the main conversation: keep the prefix predictable, send details only when needed, and check permissions at use time.

- **Stable prefix, changing suffix.** By the Way, Recap, and Suggestion reuse the main model, tool schema order, system prompt, and unchanged history where possible. Auto Memory Update reuses the completed main request, including its unchanged tool schemas, and appends the final assistant reply and the review task. Recall and AutoPilot use their own stable system instructions instead. Append the question, candidates, mission, or evidence as task data rather than interpolating it into the system prompt.
- **Fresh state over cache hits.** Use a read-only snapshot. Changes take effect on the next eligible request; never rewrite an in-flight or historical request.
- **Apply only the accepted result.** Keep Aside working messages out of main history. Apply a relevant Reminder at the next supported model boundary, or display a Notice without feeding it back to the model. A schema is not permission, and an Aside decision does not replace live runtime checks.

The host supplies the model client or provider and model ID, system prompt, and messages for each call. An Aside can borrow a main snapshot or use only its own task messages. There is no per-Aside settings editor for model or prompt overrides.

Verify provider prompt caching using reported `cache_read` and `cache_write` usage; reusing a request prefix does not guarantee a hit. A prompt-cache hit, a cached local memory index, and reuse of a completed recap are separate behaviors.

## Inspector

Model requests and responses link by recorded source. Inspector shows **Model Request · Aside — Auto Memory** and **Model Response · Aside — Auto Memory**, with **Recall** or **Update** in the details. For skill reviews, it shows **Model Request · Aside — Auto Skill** and **Model Response · Aside — Auto Skill**. These labels do not change persisted inference purposes. Keep these three views distinct: the admitted SDK request (`Tools`, `System`, `Messages`), the model's output, and the result San accepted or discarded. An output does not mean it was applied. Raw SDK request is not the provider HTTP body.

## Scope

Recap, Suggestion, and Flow Draft have a specific trigger and return a bounded result. Keep compaction under context management: it directly replaces the active context. Multi-step work with its own tools and task lifecycle belongs to a subagent or Flow run, not an Aside.

See [Context management](context-management.md), [Auto memory](../concepts/auto-memory.md), [AutoPilot](../guides/autopilot.md), and [Inspector](../guides/inspector.md).
