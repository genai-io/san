# Auto Memory

Auto Memory keeps durable facts, preferences, and decisions outside the main conversation. It is on by default and is separate from user-authored `AGENTS.md` files. The [Auto Memory Aside](../reference/aside.md) selects notes for the current question and handles explicit writes; it does not add store paths, indexes, or editing instructions to the main prompt.

## Stores

- **User:** `~/.san/memory/`, for facts that hold across projects.
- **Project:** `~/.san/projects/<encoded-main-repo-root>/memory/`. Linked worktrees and subdirectories share the same project store.

Each store contains `MEMORY.md`, an index with a short link and description per topic, and one Markdown file per topic. Notes use `name`, `description`, and `type` frontmatter. The supported types are `user`, `feedback`, `project`, and `reference`. Use absolute dates for time-sensitive facts so a note still makes sense next session.

## Recall

In the TUI, ACP, and `san -p`, the tool-free selector sees the question and candidate note IDs and descriptions. Selected notes enter the main model's request as Reminder blocks, not as Aside messages in the conversation.

- **Fast** (default): Start the main request without waiting. A ready recall can reach a later request for the same turn, or the next user turn in the same session. San checks the selected notes again before each new request, so updated text replaces old text and forgotten notes are skipped. A reminder marks results from the previous question and tells the model to ignore them unless relevant. No earlier request or reply is changed.
- **Wait**: Give recall up to the configured timeout (default `5s`) before the first main request. If recall times out or the selector fails, proceed without it.

An unchanged note already in the active context is not repeated. Pending Fast results are not restored on resume. Set the mode and timeout in `/reflection` → Auto Memory, or use `memoryRecallMode` (`fast` or `wait`) and `memoryRecallTimeout` (e.g. `"5s"`) in settings.

## Remember, update, and forget

Use an explicit maintenance instruction:

```text
remember: This project uses pnpm, not npm.
remember user memory: I prefer concise commit messages.
update project memory package-manager.md: Use pnpm 11 or newer.
forget project memory package-manager.md
```

`remember:` defaults to project memory. `update` and `forget` require the store (`project` or `user`) and exact note filename. Ordinary conversational uses of “remember” or “forget” are not write authorization. `forget` removes the stored note and its index entry. It does not erase older replies, Inspector records, or copies already admitted into the active conversation by Wait.

Maintenance reuses the last main request's prefix when it is available on the same provider and model. It appends the explicit maintenance task and cannot execute tools. With no matching prefix, it makes a small standalone request. It proposes one change, and San validates the operation, store, note, and revision before applying it. A successful write returns a display-only Notice. A no-op or failure does not claim a write succeeded. San preserves the existing Markdown format, ignores example links in literal blocks, and refuses to append an index entry inside an unclosed block. Routine recall does not write anything.

## Settings and inspection

Open `/reflection` in the TUI and select **Auto Memory**. Press Enter on User or Project to toggle that scope immediately. `/memory` (or `/memory browse`) opens the instruction files and memory stores. `/memory on|off [project]` also updates `autoMemory` directly; an explicit project setting overrides the user default and `SAN_DISABLE_AUTO_MEMORY=1` overrides both. Turning Auto Memory off starts no new recalls or maintenance. User and project instructions still apply.

`autoMemoryDirectory` can be set to an absolute or `~/` path in user settings for the user store, or untracked local project settings for the project store. Committed project settings cannot move the store.

In Inspector, **Model Request · Aside — Auto Memory** shows the admitted request. **Model Response · Aside — Auto Memory** shows the model's output and, separately, the note that was applied or the change that was accepted. The Aside's request and response are not part of the main model's history. [Auto Skill](../packages/auto-skill.md) maintains agent-owned skills and can be enabled independently.
