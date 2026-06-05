# Technical Debt

This file tracks structural follow-ups that are not tied to a single feature.

## Current Candidates

- Continue reducing root `internal/app` file size by lifecycle responsibility.
- Document extension reload behavior across skills, commands, MCP, plugins, and
  settings.

### Code refactors flagged by `docs/packages/*/Known Violations`

- **God `Service` interfaces.** All resolved.
  ~~`mcp` (9 methods)~~ — `Tools` + `Servers` + `*mcp.Registry`.
  ~~`hook` (16 methods)~~ — `Handler` + `*hook.Engine`.
  ~~`session` (11 methods)~~ — `*session.Setup` direct.
  ~~`subagent` (9 methods)~~ — `*subagent.Registry` direct.
  ~~`skill` (11 methods)~~ — `*skill.Registry` direct.
  ~~`plugin` (21 methods)~~ — `*plugin.Registry` direct + existing
  package-level free functions.
  ~~`tool` (6 methods)~~ — `*tool.Registry` direct.
  ~~`cron` (10 methods)~~ — `*cron.Scheduler` direct (renamed from
  `*Store`; methods schedule, persistence is a detail).
  ~~`task` (8 methods)~~ — `*task.Tracker` direct (renamed from
  `*Manager`; tracks background bash/subagent tasks).
  ~~`command` (7 methods)~~ — `*command.Registry` direct.
  ~~`llm` (8 methods)~~ — `*llm.Conn` direct (active provider/model/Store
  handle + `*Client` factory). Originally `*ClientFactory` wrapping a
  separate `*Setup`; the two mirror types were merged into one `Conn`
  (single mutex, unexported fields) — no wrapper remains.
  ~~`agent` (11 methods)~~ — `*agent.Task` direct (foreground agent
  task lifecycle).
  ~~`setting` (14 methods)~~ — `*setting.Settings` direct (live,
  mutex-protected handle over `*setting.Data`; also the permission
  decision gate).
- **Escape-hatch methods on Service interfaces.** All resolved:
  ~~`MCP.Registry()`~~, ~~`Hook.Engine()`~~, ~~`Session.GetStore()`~~
  / ~~`Session.SetStore()`~~ — Service interfaces deleted in their
  respective packages; consumers depend on the concrete type or a
  narrow role interface.
- ~~**`skill.AddPluginSkills` uses anonymous struct slice.**~~ Resolved
  by deleting the method entirely — it had zero callers and the
  `addPluginPath` / `additionalPaths` plumbing behind it was dead too.

### Documentation gaps (resolved 2026-05-18 in the docs/restructure branch)

- ~~`docs/guides/` only contains `explore-mode.md`.~~ Added
  `getting-started.md`, `writing-a-skill.md`, `writing-a-subagent.md`,
  `writing-a-plugin.md`.
- ~~`docs/design/decisions/` is empty.~~ Added
  `0001-layered-package-architecture.md`.
- ~~Infrastructure packages (`log`, `secret`, `filecache`, `markdown`)
  have no `docs/packages/*.md` page.~~ Added all four.

