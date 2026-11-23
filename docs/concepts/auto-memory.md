# Auto Memory

Auto Memory keeps durable facts, preferences, and decisions outside the main conversation. It is on by default. The [Auto Memory Aside](../reference/aside.md) has two operations: Recall finds relevant notes, and Update adds, edits, or forgets a note. The main model receives only recalled previews, not the directory or its index.

Project instructions are separate. Natural-language requests, in any language, go to the main agent. When you ask it to persist or change a standing project instruction, it can edit the applicable `AGENTS.md` or `AGENTS.local.md` using ordinary file tools and permissions. A reminder for the current task need not be written anywhere. `san.md` is not automatically loaded by name; see [which files are loaded](../reference/context-management.md#reminders-and-hot-reload).

## Storage and note types

Each repository has one Auto Memory directory at `~/.san/projects/<encoded-main-repo-root>/memory/`. Linked worktrees and subdirectories share it. The former user-wide `~/.san/memory/` (or its configured location) is no longer used as a global library; San leaves its files in place and does not merge them into repositories.

`MEMORY.md` is a flat index with one short link and description per topic. Each topic is a separate Markdown file with `name`, `description`, and `type` frontmatter. New index entries also show their type, for example `- [Release checklist](release.md) — (reference) Where to find the checklist`. Existing entries remain valid without a label. The four types follow [Claude Code's Auto Memory categories](https://code.claude.com/docs/en/memory#auto-memory). They describe content, not where it is stored:

| Type | What belongs here |
| --- | --- |
| `user` | The user's role, expertise, or standing preference. A personal preference saved here still stays in this repository. |
| `feedback` | How the agent should work next time: a correction or an approach the user confirmed. Include why and when it applies if known. |
| `project` | Significant ongoing work, milestones, deadlines, decisions, or constraints that aren't already recoverable from code, docs, or Git. Don't record temporary task status. |
| `reference` | Where to find a source of truth, such as an external tracker or dashboard. Save the pointer and its purpose, not a copy of the contents. |

Use absolute dates for time-sensitive facts so a note still makes sense next session.

## Index preview

By default, Recall and Update start with up to **200 complete lines or 25 KB** (25,000 bytes) of `MEMORY.md`, whichever comes first. If truncated, a separate `…` hint shows where to continue reading (`read_index`) or how to search (`search_index`). Each Aside gets two lookups, up to 8 KiB of index text per result.

Set **Preview lines** (1–2,000) and **Preview size** (1–128 KB) under `/reflect` → **Auto Memory** → **Options** (`memoryPreviewLines` and `memoryPreviewKB`). Clear a value to inherit. These settings only affect the first Aside request: they do not change the file or the separate 128 KiB index limit. The index and lookups stay out of the main conversation.

## Recall

Recall uses the index preview and current question to find linked notes that aren't already in context. The main model receives up to 2 KiB per selected note.

- **Fast** (default): Start the main request without waiting. A ready result is appended as a separate user message at the tail of the next eligible main request, after any tool results. It keeps that position on later requests so new recall does not edit the prefix. Results from an earlier question are labeled for use only when relevant. Fast retains at most ten note references within the latest 20 active user messages, excluding tool results. It rereads files on fresh requests and skips forgotten notes. The request-only context does not change native history or past requests and is not restored on resume.
- **Wait**: Give recall up to the configured timeout (default `5s`) before the first main request. If recall times out or the selector fails, proceed without it.
- **Off**: Skip recall without disabling Auto Memory or automatic Update.

Once compaction removes a note from active context, it can be selected again. In `/reflect` → **Auto Memory**, press Enter on **Recall** to cycle Fast → Wait → Off. In Wait mode, the row shows the effective timeout; press Right to focus **Wait timeout** in Options, then Enter to edit it. Settings keys are `memoryRecallMode` (`fast`, `wait`, or `off`) and `memoryRecallTimeout` (e.g. `"5s"`).

## Update

In the TUI, `/reflect memory` runs a one-off review of the latest completed main turn. Wait for the current reply to finish; the completed turn must match the current provider and model. The Auto Memory store must be enabled, but automatic Update can be off.

The review reuses the completed main request's exact SDK `System`, ordered `Tools`, and native `Messages`, followed by its final assistant message. It extends a separate request in the same session:

1. **Check the index.** If the preview is truncated, search omitted entries before adding a note. Malformed links or duplicate note filenames disable additions; if nothing changes, `/reflect memory` reports why and automatic runs log it. New labels cannot mention another note filename.
2. **Read on demand.** Update can request two indexed notes per round, for up to two rounds (8 KiB per note, 16 KiB total).
3. **Propose one change or none.** Update can add, revise, or forget a note. It must read a note before revising or forgetting it. When the user clearly asks to forget a note, Update uses the existing `Bash` (`rm`, also under zsh) or `PowerShell` (`Remove-Item -LiteralPath`) call if available. San validates the single filename and removes both the note and its link without running a shell. If neither tool is available, Update uses the JSON `forget` action. The tool catalog stays unchanged.

To review automatically after an eligible completed turn, enable **Update** under `/reflect` → **Auto Memory**. It is off by default (`memoryUpdate: false`). Both manual and automatic runs appear as **Aside — Auto Memory → Update** in Inspector.

Update runs in the background without blocking the main agent. It doesn't edit project instruction files, and its working messages and results aren't sent to the main model. San shows a display-only notice after a successful write. A manual review that finds nothing to change reports `No memory changes.` A no-op automatic review is quiet. San preserves the index format, ignores example links in literal blocks, and refuses to append inside an unclosed block. Forget removes a note from its store; it does not erase old replies or Inspector records. Recall itself never writes.

## Settings and inspection

Open `/reflect` → **Auto Memory** for **Recall** and **Update**. Right opens **Options**, where the master switch pauses both without resetting their settings. `/memory` (or `/memory browse`) opens the instruction files and repository memory. `/memory on` and `/memory off` also control the master switch, and `SAN_DISABLE_AUTO_MEMORY=1` overrides it. Turning it off starts no new recalls or updates. User and project instruction files still apply.

Set `autoMemoryDirectory` to an absolute or `~/` path in the main checkout's untracked `.san/settings.local.json` to use a different directory for the whole repository. Settings checked into Git, settings in linked worktrees, and the former user-store setting cannot redirect it.

The TUI shows one muted notice when the main model first receives new Fast recall context. Inspector shows the selector's admitted request and response separately; open the main request to see where the selected previews were attached. Neither the Aside messages nor the TUI notice are fed back to the main model. [Auto Skill](../packages/auto-skill.md) maintains agent-owned skills and can be enabled independently.
