---
package: github.com/genai-io/san/internal/reflect/skill
layer: feature
---

# Auto Skill

Auto Skill turns a useful workflow from a completed turn into an agent-owned skill. It uses an [Aside](../reference/aside.md), not the main model's system prompt or a separate review session. It does not save facts as memory; that is [Auto Memory](../concepts/auto-memory.md).

## Settings

Open `/reflect` and use Tab to switch to **Auto Skill**. Auto Skill is independent of Auto Memory. It requires at least one allowed skill action and an enabled `ReviewSkill` tool. Use `ReviewSkill` in tool rules and hook matchers. The panel edits the strategy and permissions (create, update, delete) and lets you browse learned skills. Choose a save scope and select **Save** to apply changes. Settings are saved under `autoSkill`; existing `selfLearn` settings are read for compatibility.

## Review

1. The main agent can call `ReviewSkill` to flag a turn worth reviewing. A review starts only after a clean turn end whose latest user message has user-authored text (not only a system reminder); cancelled and incomplete turns do not trigger it. Only one review runs at a time.
2. The Aside sends its own stable system prompt, the configured strategy, a bounded excerpt of the completed turn, and relevant skill information. It sends no executable tools. A turn without skill use can propose a new skill; one that used a skill can propose an update or retirement within the configured permissions.
3. `SkillManager` validates and applies at most one proposal. Changes to existing skills require agent-owned provenance and a matching revision. User-authored skills are not editable through Auto Skill.
4. A saved change appears in the session as a display-only notice and in the learned inventory. Inspector pairs **Model Request · Aside — Auto Skill** with **Model Response · Aside — Auto Skill** and shows what was accepted. The review messages do not enter main history.

The review ends with the originating session: clear or session replacement cancels pending work. The learned skill remains on disk once saved.
