# Auto Memory

Auto Memory keeps durable facts, preferences, and decisions outside the main conversation. It is on by default and is separate from user-authored `AGENTS.md` files. The [Auto Memory Aside](../reference/aside.md) selects notes for the current question and handles explicit writes; it does not add store paths, indexes, or editing instructions to the main prompt.

## Stores

- **User:** `~/.san/memory/`, for facts that hold across projects.
- **Project:** `~/.san/projects/<encoded-main-repo-root>/memory/`. Linked worktrees and subdirectories share the same project store.

Each store contains `MEMORY.md`, an index with a short link and description per topic, and one Markdown file per topic. Notes use `name`, `description`, and `type` frontmatter. The supported types are `user`, `feedback`, `project`, and `reference`. Use absolute dates for time-sensitive facts so a note still makes sense next session.

## Recall

In the TUI, ACP, and `san -p`, San runs a short, tool-free recall before sending an eligible user input to the main model. The selector sees the question and candidate note IDs and descriptions. San reads the selected notes and appends them as separate Reminder blocks to the same user message. A turn can receive memory even if the main model calls no tools.

If there are no notes, nothing is sent to the selector. If recall times out, the main turn continues without it; no late result is attached to a different turn. An unchanged note already in the active context is not repeated. A changed note can be recalled again. Existing transcripts are not rewritten when memory is disabled or updated.

## Remember, update, and forget

Use an explicit maintenance instruction:

```text
remember: This project uses pnpm, not npm.
remember user memory: I prefer concise commit messages.
update project memory package-manager.md: Use pnpm 11 or newer.
forget project memory package-manager.md
```

`remember:` defaults to project memory. `update` and `forget` require the store (`project` or `user`) and exact note filename. Ordinary conversational uses of “remember” or “forget” are not write authorization.

Maintenance sends no tools to the model. It proposes one change, and San validates the operation, store, note, and revision before applying it. A successful write returns a display-only Notice. A no-op or failure does not claim a write succeeded. San preserves the existing Markdown format and updates the affected index entry. Routine recall does not write anything.

## Settings and inspection

Open `/reflection` in the TUI and select **Auto Memory**. Press Enter on User or Project to toggle that scope immediately. `/memory` (or `/memory browse`) opens the instruction files and memory stores. `/memory on|off [project]` also updates `autoMemory` directly; an explicit project setting overrides the user default and `SAN_DISABLE_AUTO_MEMORY=1` overrides both. Turning Auto Memory off starts no new recalls or maintenance. User and project instructions still apply.

`autoMemoryDirectory` can be set to an absolute or `~/` path in user settings for the user store, or untracked local project settings for the project store. Committed project settings cannot move the store.

In Inspector, **Model Request · Aside — Auto Memory** shows the admitted request. **Model Response · Aside — Auto Memory** shows the model's output and, separately, the note that was applied or the change that was accepted. The Aside's request and response are not part of the main model's history. [Auto Skill](../packages/auto-skill.md) maintains agent-owned skills and can be enabled independently.
