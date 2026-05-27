# Design Principles

## Documentation

- Keep short contributor navigation in `docs/agent-guide.md`; reserve
  `AGENTS.md` for Codex-compatible runtime instructions.
- Keep durable explanations in `docs/`.
- Prefer a single obvious index for each documentation area.
- Document package ownership and dependency direction before large refactors.
- Per-package details belong in `docs/packages/<pkg>.md`. Promote a topic
  to `docs/concepts/` only when three or more packages collaborate
  around the idea and the collaboration must be understood before any
  single package's docs make sense. Single-package internals stay in
  that package's page.

## Code Structure

- Favor cohesive capability packages over abstract layer names.
- Keep `internal/app` as composition, not as a place for feature behavior.
- Keep `internal/core` stable and dependency-light.
- Move shared contracts downward only when multiple packages need them.
- Avoid large rewrites when a file split or small interface will solve the
  coupling problem.
