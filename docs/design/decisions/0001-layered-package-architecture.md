# ADR-0001: Layered package architecture with per-package Contract docs

## Status

Accepted — 2026-05-18.

## Context

Before this decision, the repository had:

- A flat `internal/` tree with 25+ packages and no declared layering.
- A `docs/` tree mixing system overviews, per-component deep dives, and
  numerically-prefixed "feature" notes (`features/1-…`, `features/2-…`,
  …) that had grown chronologically rather than by reader goal.
- Each package exposed a kitchen-sink `Service` interface bundling every
  method the concrete type happened to have, with several "escape hatch"
  methods (`Engine()`, `Registry()`, `GetStore()`) that let callers
  bypass the interface entirely.
- No mechanical enforcement of dependency direction.
- `CONTRIBUTING.md` referenced `internal/provider/`, `internal/config/`,
  `internal/ui/` — none of which existed.

The state was readable for the original authors but expensive for new
contributors and agents to navigate. Documentation regularly drifted
from code because there was no canonical "per-package" document, and no
single source of truth for what depended on what.

## Decision

Adopt a layered, contract-first model with five layers and per-package
specification docs:

1. **Five layers**, dependency direction strictly `cmd → app → feature
   → core → infrastructure`. Layer assignment for every package
   recorded in [`reference/package-map.md`](../../reference/package-map.md);
   rules and the layer membership table in
   [`reference/dependency-rules.md`](../../reference/dependency-rules.md).
   Enforced by `tools/layercheck`, wired into `make lint`.

2. **One `docs/packages/<pkg>.md` page per Go package with a non-trivial
   public surface.** The filename matches the package name with no
   suffix. Pages must follow
   [`packages/TEMPLATE.md`](../../packages/TEMPLATE.md) and include a
   verbatim copy of the package's public Go interface in a `Contract`
   section. Seven small-interface rules govern what may live in
   `Contract`.

3. **Six-bucket documentation layout** organized by reader goal:

   | Goal | Bucket |
   |---|---|
   | Understand the system | `architecture.md`, `packages/`, `concepts/` |
   | Look up a fact | `reference/` |
   | Accomplish a task | `guides/` |
   | Maintain the repo | `operations/` |
   | Know why a decision was made | `design/decisions/` |

4. **Cross-cutting topics that don't fit a single package live in
   `concepts/`** (extension model, harness channels, permission model).
   Single-package internals stay in that package's `Internals` /
   `Lifecycle` sections.

5. **Work-in-progress plans live at the repo root in `notes/`**, not in
   `docs/`. Docs are the durable knowledge base; plans are ephemeral.

## Consequences

### Positive

- **One page per package** maps cleanly to the code tree. A reader who
  knows the code can find the doc, and vice versa.
- **Contract sections** make the seam of each package machine-readable.
  Eventually a doc linter can compare the contract block against
  `go doc -short ./internal/<pkg>` to flag drift.
- **Mechanical layer enforcement** via `tools/layercheck` runs in CI.
  Caught one real violation immediately: `internal/image` was importing
  `internal/core`.
- **Reader-goal layout** matches Diátaxis pragmatically without forcing
  unfamiliar names. New contributors find what they need without
  reading 18 architecture files in sequence.
- **`Known Violations` audit per package** surfaces the codebase's
  Service-pattern debt without burying it in commit history. PR-3 has
  a complete to-do list in `notes/tech-debt.md`.

### Negative / costs

- **Writing discipline required.** Each new package needs a page; each
  contract change needs a doc change. The
  [`packages/TEMPLATE.md`](../../packages/TEMPLATE.md) is the friction
  surface that keeps quality consistent.
- **Documentation gaps are now visible.** Empty `guides/` and
  `decisions/` directories make the "missing" content obvious. (Most
  of the initial gaps were filled in the same PR; the rest tracked in
  `notes/tech-debt.md`.)
- **`tools/layercheck` becomes load-bearing.** Updates to package
  layout require updating `package-map.md` and, when layer rules change,
  `dependency-rules.md`. `layercheck` reads `package-map.md` directly so
  the package-to-layer assignment has one source of truth.
- **Provisional classifications.** `internal/image` is marked `feature`
  because it touches `core.Image`; the cleaner "infrastructure +
  adapter" split is in `notes/tech-debt.md`.

### Code-level follow-ups (not in this ADR)

The new doc structure made the existing god-`Service` pattern obvious.
A separate PR-3 addresses:

- Splitting kitchen-sink `Service` interfaces into 2–4 narrow
  consumer-defined interfaces per package.
- Dropping `Engine()` / `Registry()` / `GetStore()` escape hatches.
- Moving construction from per-package `Default()` singletons into the
  `cmd/san` composition root.

Full road map in `notes/tech-debt.md`.

## References

- [`docs/concepts/architecture.md`](../../concepts/architecture.md) — the new
  system-level overview.
- [`docs/packages/TEMPLATE.md`](../../packages/TEMPLATE.md) — the
  binding template for new package pages.
- [`docs/packages/index.md`](../../packages/index.md) — index of every
  per-package page.
- [`docs/reference/dependency-rules.md`](../../reference/dependency-rules.md)
  — layer rules.
- [`docs/reference/package-map.md`](../../reference/package-map.md) —
  package-to-layer assignment.
- [`tools/layercheck/main.go`](../../../tools/layercheck/main.go) —
  mechanical enforcement.
- OpenAI's "Harness Engineering" post (Feb 2026) — the broader frame
  that motivated this approach.
