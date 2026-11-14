# ACP acceptance and retention follow-ups

Status: implementation complete; external acceptance and long-lived profiling open
(2026-11-14). Design: [PROP-0004](../../docs/design/proposal-0004-acp-agent-entrypoint.md).

## Completed delivery

The ACP host and unified native session cutover are implemented, including
San #764–766. Numeric tool-schema regression fixes are merged in sdk-go #112
and San #767. Current behavior, test entrypoints and declared unsupported
capabilities belong in [the host documentation](../../docs/packages/app.md#acp-stdio-host);
those limits are not migration TODOs. Native storage delivery and repository-gate
results are archived in [session cutover](../completed/session-cutover.md).

The earlier restart-versus-reuse allocation table predates the unified cutover
and is retired as a current baseline. Allocation counts and scripted-provider
lifecycle tests do not establish heap-retention behavior or editor acceptance.

## Remaining validation

Owner: ACP host maintainers; no editor/provider acceptance or heap-profile result
is claimed by the implementation completion.

- **Real editor and live-provider acceptance (not performed).** Record client,
  provider and versions; exercise new/load, prompt streaming, tool approval,
  cancellation followed by another prompt, and disconnect cleanup. Acceptance:
  supported flows complete without protocol leakage or orphaned work; record
  observed failures and capability negotiation against the declared host limits.
  Use authorized local credentials without recording secrets.
- **Long-lived heap-retention profiling (not performed).** Capture a post-cutover
  baseline and heap profiles across repeated prompts, cancellation, load and
  session close. Acceptance: distinguish expected live-session history from
  retained closed-session runtimes, histories, hooks and task output; document
  retained paths and investigate unexplained growth. Short allocation benchmarks
  are not a substitute.
