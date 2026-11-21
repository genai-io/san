# San Agent Guide

Keep this file to repository-wide rules and pointers. Inspect the checkout for
its current structure; use the package map for ownership. Put local conventions
in the nearest `AGENTS.md` and private notes in `AGENTS.local.md` (git-ignored).

## Where to look

- [Docs and feature index](docs/index.md)
- [Package ownership](docs/reference/package-map.md)
- [Build, test, lint, and format](docs/operations/development.md)

## Before changing code

- Read the [dependency rules](docs/reference/dependency-rules.md) and
  [design principles](docs/design/principles.md) before editing internal packages.
- Keep San-specific policy and UI here; put reusable agent primitives in
  [sdk-go](https://github.com/genai-io/sdk-go). Do not duplicate SDK behavior.
- Update relevant docs alongside architecture or workflow changes. Keep docs and
  comments concise; explain behavior and constraints, not directory listings or
  implementation history. See [file naming](docs/reference/file-naming.md) and
  [design records](docs/design/index.md).
- Keep active plans in `notes/active/` and completed plans in `notes/completed/`.
