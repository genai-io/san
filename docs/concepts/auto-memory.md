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

## Recall

Each tool-free Recall request has three user messages: the file index (IDs and descriptions), the IDs already in active main context, and the current question. The index is not ranked or truncated by the question, so it remains a reusable prefix while the note metadata is unchanged. The selector does not replay past Aside messages or select already-covered files again. The main model receives at most 2 KiB read from each selected file as a Reminder.

- **Fast** (default): Start the main request without waiting. A ready result is appended as a separate user message at the tail of the next eligible main request, after any tool results. It keeps that position on later requests so new recall does not edit the prefix. Results from an earlier question are labeled for use only when relevant. Fast retains at most ten note references within the latest 20 active user messages, excluding tool results. It rereads files on fresh requests and skips forgotten notes. The request-only context does not change native history or past requests and is not restored on resume.
- **Wait**: Give recall up to the configured timeout (default `5s`) before the first main request. If recall times out or the selector fails, proceed without it.
- **Off**: Skip recall without disabling Auto Memory or automatic Update.

Once compaction removes a note from active context, it can be selected again. In `/reflection` → **Auto Memory**, press Enter on **Recall** to cycle Fast → Wait → Off. In Wait mode, the row shows the effective timeout; press Right to focus **Wait timeout** in Options, then Enter to edit it. Settings keys are `memoryRecallMode` (`fast`, `wait`, or `off`) and `memoryRecallTimeout` (e.g. `"5s"`).

## Update

In the TUI, `/memory update` runs a one-off review of the latest eligible completed main turn, even when automatic Update is off. Auto Memory must be enabled.

The review reuses the completed request's exact SDK `System`, ordered `Tools`, and native `Messages`, then its final native assistant message. San appends one user message containing the review instructions, the repository's `MEMORY.md`, and bounded note bodies linked from that index. The copied tool definitions and historical calls are context only: the Aside cannot execute tools.

The model proposes at most one action: add a note and index link, revise an indexed note without moving it or changing its type, forget a note and its link, or make no change. San validates the target and file revision before writing to the repository store. Forget requires clear user intent to remove a specific note.

To review automatically after an eligible completed turn, enable **Update** under `/reflection` → **Auto Memory**. It is off by default (`memoryUpdate: false`). Both manual and automatic runs appear as **Aside — Auto Memory → Update** in Inspector.

Update runs in the background without blocking the main agent. It doesn't edit project instruction files, and its working messages and results aren't sent to the main model. San shows a display-only notice after a successful write, preserves the existing index format, ignores example links in literal blocks, and refuses to append inside an unclosed block. Forget removes a note from its store; it does not erase old replies or Inspector records. Recall itself never writes.

## Settings and inspection

Open `/reflection` → **Auto Memory** for **Recall** and **Update**. Right opens **Options**, where the master switch pauses both without resetting their settings. `/memory` (or `/memory browse`) opens the instruction files and repository memory. `/memory on` and `/memory off` also control the master switch, and `SAN_DISABLE_AUTO_MEMORY=1` overrides it. Turning it off starts no new recalls or updates. User and project instruction files still apply.

Set `autoMemoryDirectory` to an absolute or `~/` path in the main checkout's untracked `.san/settings.local.json` to use a different directory for the whole repository. Settings checked into Git, settings in linked worktrees, and the former user-store setting cannot redirect it.

The TUI shows one muted notice when the main model first receives new Fast recall context. Inspector shows the selector's admitted request and response separately; open the main request to see where the selected previews were attached. Neither the Aside messages nor the TUI notice are fed back to the main model. [Auto Skill](../packages/auto-skill.md) maintains agent-owned skills and can be enabled independently.
