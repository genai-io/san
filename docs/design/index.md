# Design

Why the system is shaped the way it is. Two kinds of document live here, and
they are written at different times.

| | Proposal | Decision record (ADR) |
|---|---|---|
| Answers | should we build this, and how? | what did we settle, and why? |
| Written | before the work | after it ships |
| Length | as long as the design needs | about a page |
| Lifecycle | edited while the work is under way | superseded, never edited |
| Carries | goals, alternatives, phased plan, open questions | context, decision, consequences |

Write the proposal first. When the work ships, document the resulting
behaviour in [`../concepts/`](../concepts/index.md); the proposal stays as the
record of how the design was arrived at.

An ADR is worth adding only when the rationale has no other home — when the
concept page would otherwise grow a long justification section that is not
about using the feature. Not every shipped proposal needs one, and an ADR
written before the work exists is a proposal wearing the wrong template.

A third kind of document lives outside `docs/`: a `notes/` plan is an
execution checklist — what is being done, in what order, what is finished. A
sizable feature usually has both a proposal and a notes plan.

## Proposals

- [Workflow orchestration](proposal-0001-workflow-orchestration.md) · [中文](proposal-0001-workflow-orchestration.zh.md) — Core implemented. Optional full-screen graph remains future design.
- [Autonomous development management](proposal-0002-autonomous-dev-management.md) · [中文](proposal-0002-autonomous-dev-management.zh.md) — Draft. A persona team that manages the project end to end.
- [Session groups](proposal-0003-session-groups.md) · [中文](proposal-0003-session-groups.zh.md) — Implemented. Local sessions join groups and message each other.
- [ACP agent entrypoint](proposal-0004-acp-agent-entrypoint.md) — Baseline implemented. Independent editor/live-provider acceptance and long-lived heap profiling remain open.
- [Session management and file-backed persistence](proposal-0005-session-management.md) · [中文](proposal-0005-session-management.zh.md) — Implemented. Unified native storage and hosts; [completed cutover](../../notes/completed/session-cutover.md) records validation and deferred follow-ups.

## Decision records

- [Layered package architecture](decision-0001-layered-package-architecture.md) — Accepted. Five layers, dependency direction, and per-package contracts.
- [Native scrollback commit protocol](decision-0002-native-scrollback-commit-protocol.md) — Accepted. Invariants that keep live frame rows out of native scrollback.

## Principles

[`principles.md`](principles.md) — engineering principles for structure and
documentation.

## Conventions

Keep both series directly in `docs/design/`, named `proposal-NNNN-topic.md`
and `decision-NNNN-topic.md`. Number each series from `0001` independently;
a proposal that later earns an ADR does not share its number. Translations
are co-located with a `.zh.md` suffix next to the English page.

### Proposal template

```markdown
# PROP-0000: Title

## Status
Draft | Accepted | Implemented | Withdrawn

## Motivation
## Goals
## Non-Goals
## Design
## Alternatives considered
## Risks and trade-offs
## Implementation plan
## Open questions
## References
```

### Decision record template

```markdown
# ADR-0000: Title

## Status
Proposed | Accepted | Superseded

## Context
## Decision
## Consequences
## References
```
