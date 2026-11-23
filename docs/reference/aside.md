# Aside

An Aside is a focused model call outside the main conversation. It can use a read-only snapshot of the main context or build its own request. Its working messages do not enter main history. Only the accepted result is returned: a display-only Notice, model-facing context, an editable draft, or a decision for the runtime to validate.

The diagram shows the main delivery paths. Inspector records the requests and responses separately from their accepted results.

```text
Trigger                      Aside                 Result
User asks a question      -> Auto Memory / Recall   -> Reminder in an eligible main request
Completed turn or command -> Auto Memory / Update  -> Validated store change, then display-only Notice
User asks /btw            -> By the Way             -> Display-only Notice
Eligible turn ends        -> AutoPilot              -> Proposed next step; runtime decides
ReviewSkill and a clean turn   -> Auto Skill             -> Validated skill change, then Notice
```

Asides stay in the same session for Inspector. Only accepted model-facing results enter the main context.

| Aside | Trigger | Tools in the request | System prompt and messages | Result |
|---|---|---|---|---|
| **Auto Memory · Recall** | A user message with enabled memory and candidates not already in active main context. | None. San reads the repository store locally. | A selection-only system prompt and three user messages: file index, already-covered IDs, and the current question. | The model returns up to five new note IDs. San attaches bounded previews to an eligible user message in the main request. |
| **By the Way** | Explicit `/btw <question>`. | The active main conversation's tool schemas, including enabled MCP tools, for prefix reuse. The Aside cannot execute them. | Main system prompt and active history, then a separate user message with the side instruction and question. | A text answer shown as a Notice, not sent to the main model. |
| **AutoPilot** | An enabled decision point, such as turn end, an eligible approval review, or a user-question steer. | None. A tool under review is input data, not an available tool. | A decision-specific prompt and one task message with bounded evidence, such as the mission, recent progress, or eligible choices. | A structured decision. The runtime may accept it only under existing permissions and continuation limits. |
| **Auto Skill** | At least one skill action is allowed, and a clean turn ends after a `ReviewSkill` request. | None. | A review prompt, configured strategy, and a bounded excerpt of the settled turn and relevant skills. | San validates at most one change to an agent-owned skill. The change is recorded separately from the main model's history. |
| **Recap** | After an eligible absence, either prepared while away or requested when the user returns. | Main tool schemas for reuse; none executed. | Main system prompt and history, then a short recap instruction and relevant status facts. | A brief status and next action shown in the UI. A prepared recap is reused only while it still describes the current conversation. |
| **Suggestion** | When suggestions are enabled and a completed turn leaves enough context; deferred while the terminal is unfocused. | Main tool schemas for reuse; none executed. | Main system prompt and history, then an instruction to suggest the next input; includes the active mission when applicable. | An editable input hint, never submitted automatically. |
| **Flow Draft** | Explicit `/flow <goal>`. | None. | Main system prompt and history, then the drafting instructions, available agents and skills, and the user's goal. | An unsaved draft in Review. Saving and running are separate user actions. |

## Auto Memory lifecycle

Recall and Update use the same repository store but have different triggers and outputs. Neither can execute tools.

| Operation | Trigger | Task message | Accepted result |
|---|---|---|---|
| **Recall** | **Fast** starts in parallel with the main request. **Wait** gives recall up to the configured timeout before the first request. | Send the current file index, already-covered IDs, and question. The index contains descriptions, not note bodies. | Selected notes reach an eligible main request as Reminders. A late result from the previous question is labeled as such. A failed selector does not prevent the turn. |
| **Update** | `/memory update`, or an eligible completed turn when automatic Update is on. | After the exact main SDK `System`, ordered `Tools`, native `Messages`, and final assistant message, append one user message with review instructions, `MEMORY.md`, and bounded linked note bodies. Copied tools cannot execute. | San validates and applies at most one proposed add, revision, or removal to the repository store. A successful write produces a display-only Notice. |

Natural-language input is never routed to Update based on its wording. The main agent handles project instructions through ordinary files and permissions. Update runs outside the main turn and does not manage those files. Its working messages, store paths, indexes, and status stay out of the main context. A disabled store starts no new Recall or Update work. `/reflection` opens separate Auto Memory and Auto Skill tabs; `/memory` opens the store and instruction-file browser.

## Context and caching

Asides follow the same [Context Management principles](context-management.md#principles) as the main conversation: keep the prefix predictable, send details only when needed, and check permissions at use time.

- **Stable prefix, changing suffix.** By the Way, Recap, and Suggestion reuse the main model, tool schema order, system prompt, and unchanged history where possible. Auto Memory Update reuses the completed main request and its final assistant output. Recall and AutoPilot use their own stable system instructions instead. Append the question, candidates, mission, or evidence as task data rather than interpolating it into the system prompt.
- **Fresh state over cache hits.** Capture the request from a read-only snapshot. A reload, changed tool list, or updated memory index takes effect at the next eligible request; never rewrite an in-flight or historical request. Recall sends filenames and descriptions in deterministic order, without note bodies.
- **Apply only the accepted result.** Keep Aside working messages out of main history. Apply a relevant Reminder at the next supported model boundary, or display a Notice without feeding it back to the model. A schema is not permission, and an Aside decision does not replace live runtime checks.

The host supplies the model client or provider and model ID, system prompt, and messages for each call. An Aside can borrow a main snapshot or use only its own task messages. There is no per-Aside settings editor for model or prompt overrides.

Verify provider prompt caching using reported `cache_read` and `cache_write` usage; reusing a request prefix does not guarantee a hit. A prompt-cache hit, a cached local memory index, and reuse of a completed recap are separate behaviors.

## Inspector

Model requests and responses link by recorded source. Inspector shows **Model Request · Aside — Auto Memory** and **Model Response · Aside — Auto Memory**, with **Recall** or **Update** in the details. For skill reviews, it shows **Model Request · Aside — Auto Skill** and **Model Response · Aside — Auto Skill**. These labels do not change persisted inference purposes. Keep these three views distinct: the admitted SDK request (`Tools`, `System`, `Messages`), the model's output, and the result San accepted or discarded. An output does not mean it was applied. Raw SDK request is not the provider HTTP body.

## Scope

Recap, Suggestion, and Flow Draft have a specific trigger and return a bounded result. Keep compaction under context management: it directly replaces the active context. Multi-step work with its own tools and task lifecycle belongs to a subagent or Flow run, not an Aside.

See [Context management](context-management.md), [Auto memory](../concepts/auto-memory.md), [AutoPilot](../guides/autopilot.md), and [Inspector](../guides/inspector.md).
