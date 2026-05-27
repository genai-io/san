# Dependency Rules

Use these rules when adding imports or moving code. They are intentionally simple
so they can become automated checks later.

## Layers

Every package is assigned exactly one layer. `package-map.md` records the
assignment for each package.

| Layer | Members | Role |
| --- | --- | --- |
| `cmd` | `cmd/*` | Process entrypoints, flag parsing, service wiring. |
| `app` | `internal/app` and its subpackages | TUI shell, model composition, event routing. |
| `feature` | Business-domain packages: agent, instruction, session, hook, skill, plugin, mcp, command, tool, subagent, task, cron, identity, inspector, llm, search, worktree, setting, reminder, image | Cohesive product capabilities with their own state and lifecycle. |
| `core` | `internal/core` | Stable contracts shared across feature packages. |
| `infrastructure` | `log`, `secret`, `filecache`, `markdown` | Stateless helpers usable by any layer above. |

`internal/image` is provisionally classified as `feature` because it
currently produces `core.Image` values directly. The pure-infra
extraction (a separate adapter consuming both `image` and `core`) is
tracked in `notes/tech-debt.md`.

## Layer Direction

```text
cmd
  -> app
     -> feature
        -> core
        -> infrastructure
```

Allowed upward composition happens through interfaces and constructor arguments,
not by lower layers importing higher layers.

## Rules

1. `cmd/*` may import `internal/app` and package-level services needed for CLI
   subcommands. Command packages should not own long-running business logic.
2. `internal/app` may import feature packages because it is the composition
   layer. App subpackages should expose small runtime interfaces instead of
   importing the root app package.
3. `internal/core` must stay dependency-light. It should not import `app`,
   `session`, `tool` implementations, provider packages, or extension packages.
4. Feature packages should not import `internal/app`.
5. Extension packages should own their registries and loaders. Cross-extension
   composition belongs in `internal/app` or a narrow integration function.
6. Tool implementation packages under `internal/tool/*` should remain adapters.
   Complex behavior belongs in the feature package that owns the capability.
7. Infrastructure helpers such as `log`, `secret`, `markdown`, and `filecache`
   should not import feature packages.
8. Provider implementations should register with `internal/llm`; product logic
   should not depend directly on concrete provider packages.

## Preferred Techniques

- Define interfaces at the consumer side.
- Put shared contracts in `internal/core` only when multiple packages need the
  same stable type.
- Split large files by lifecycle or responsibility before splitting packages.
- Prefer constructors and dependency structs over runtime singleton lookups.
- Keep tests in the package that owns the behavior unless the test is explicitly
  cross-package integration coverage.

## Review Checklist

- Does the new import point from a higher layer to a lower layer?
- Could the dependency be replaced by a small interface?
- Is this package now responsible for more than one capability?
- Should a feature document or architecture document be updated?
- Is this change adding a new singleton access path that should be injected?
