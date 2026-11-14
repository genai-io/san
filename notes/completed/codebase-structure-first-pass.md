# Codebase Structure First Pass

Status: completed; archived 2026-11-14. Current navigation and ownership live in
[the documentation index](../../docs/index.md), [package map](../../docs/reference/package-map.md)
and [dependency rules](../../docs/reference/dependency-rules.md).

## Goal

Make the repository easier for engineers and agents to navigate before starting
larger package or file refactors.

## Scope

- Add short top-level navigation for agents and contributors.
- Add durable documentation indexes under `docs/`.
- Document current package responsibilities.
- Document dependency direction and review rules.
- Preserve existing feature document filenames for now to avoid broken links.

## Completed In This Pass

- Added `AGENTS.md`.
- Added `docs/index.md`.
- (superseded) Added focused architecture stubs under `docs/architecture/`. Folded into `docs/packages/` and `docs/concepts/` in PR-2.
- (superseded) Added `docs/features/index.md`. Replaced by `docs/packages/index.md` in PR-2.
- Added design, operations, references, and plans sections.
- Updated README documentation links.
- Renamed numbered feature documents to stable kebab-case names and updated
  links.
- Renamed ambiguous deep-dive documents to descriptive names and added
  `docs/reference/file-naming.md`.
- Added `tools/layercheck` enforcement and later made it read package-layer
  assignments directly from `docs/reference/package-map.md`.

## Later maintenance (not part of this completed pass)

The old UI-document split candidate is superseded by `docs/packages/app.md` and
`docs/concepts/`. Further lifecycle file splits are ongoing maintenance, not a
reason to keep this navigation pass active. Track concrete remaining structural
work in [technical debt](../tech-debt.md), with package ownership in the package map.
