# File Naming

Use predictable names so files are easy to find by humans, search tools, and
agents.

## General Rules

- Markdown documents use lower-case kebab-case: `agent-core.md`.
- Go files use lower-case snake_case when multiple words are needed:
  `message_convert.go`.
- Directories use lower-case names. Use kebab-case for documentation
  directories only when a multi-word directory is unavoidable.
- Do not use ordering prefixes such as `01-` unless the files are a deliberately
  sequenced tutorial.
- Avoid a file and directory with the same base name in the same area, such as
  `docs/architecture.md` and `docs/packages/`.
- Prefer descriptive capability names over generic names: `agent-core.md`
  instead of `core.md`.

## Accepted Exceptions

- Standard root files keep conventional uppercase names: `README.md`,
  `CONTRIBUTING.md`, `CHANGELOG.md`.
- External instruction compatibility files keep their required uppercase
  names: `GEN.md`, `CLAUDE.md`, `AGENTS.md`.
- Skill definitions keep the required `SKILL.md` name.
- Versioned manuals may include semantic versions, for example
  `manual-v1.17.md`.
- Generated or external-compatibility templates may keep their required names,
  such as `README.md.tmpl`.

## Documentation Families

- System overview lives in `docs/architecture.md`; per-package design in `docs/packages/`.
- Cross-cutting concepts live in `docs/concepts/`.
- Operational runbooks live under `docs/operations/`.
- Stable references live under `docs/reference/`.
- Active and completed plans live under `notes/` (repo root).
