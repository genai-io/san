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

| Page | Status | What it proposes |
|---|---|---|
| [`0001-workflow-orchestration`](proposals/0001-workflow-orchestration.md) | Draft | Subagent workflows: an acyclic graph defined in markdown, with a mermaid flowchart for topology. (中文: `.zh.md`) |
| [`0002-autonomous-dev-management`](proposals/0002-autonomous-dev-management.md) | Draft | A team of personas that manages the project end to end. (中文: `.zh.md`) |

## Decision records

| Page | Status | What it settled |
|---|---|---|
| [`0001-layered-package-architecture`](decisions/0001-layered-package-architecture.md) | Accepted | Five layers, enforced dependency direction, one contract page per package. |

## Principles

[`principles.md`](principles.md) — engineering principles for structure and
documentation.

## Conventions

Number both series from `0001` independently; a proposal that later earns an
ADR does not share its number. Translations are co-located with a `.zh.md`
suffix next to the English page, the same rule the rest of `docs/` follows.

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
