---
name: audit
description: Review San's model context and engineering quality. Run both reviews by default, or choose one.
argument-hint: "[context|engineering] [scope or focus]"
---

# Audit San

Keep the review read-only. Treat arguments other than the review type as the scope or focus.

- `/audit`: Follow both guides. Report the results in separate sections for model context and engineering quality.
- `/audit context`: Follow the [context guide](references/context.md). Review what San sends the model and how changes affect task quality, cache use, and branch costs. Report in Chinese by default.
- `/audit engineering`: Follow the [engineering guide](references/engineering.md). Review correctness, security and data integrity, concurrency and scale, tests, performance, unnecessary complexity, and maintainability under the expected workload. Review the whole repository unless the user narrows the scope. Report in plain English.

Use the same language defaults when reporting both reviews together.
