# ACP follow-ups

Implementation complete (2026-11-14). See [host behavior](../../docs/packages/app.md#acp-stdio-host)
and the [completed cutover](../completed/session-cutover.md).

Owner: ACP host maintainers. Both checks below remain unperformed:

- **Editor/live-provider acceptance:** cover new/load, streaming, approvals,
  cancel/continue and disconnect cleanup. Record versions, results and negotiated limits.
- **Heap retention:** profile repeated prompts, cancellation, load and close;
  verify closed sessions release runtimes, history, hooks and task output.

Use authorized credentials and keep secrets out of reports.
