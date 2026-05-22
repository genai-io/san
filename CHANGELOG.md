# Changelog

All notable changes to Gen Code are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/).

## [v1.19.0] - 2026-05-23

### Added
- Welcome splash screen with ❭ input prompt glyph
- "auto" theme to match terminal appearance automatically (zhujian)
- Persist thinking effort per model across launches
- Concept documentation: data-flow (en + zh), rendering (en + zh)

### Changed
- Rotate the thinking spinner and agent task indicator instead of flickering
- Cancel/interrupt flow: quiescence handshake, pending latch, defensive fixes across agent/llm/conv layers
- Refactor app model: split model.go (1103 lines) and update.go (821 lines)
- Collapse submit-path indirection; centralize agent submission into SubmitToAgent
- Drop Service interfaces across 7 packages; use concrete types (session, subagent, skill, plugin, mcp, hook)
- Rename Command* → SlashCommand*, overlay → popup, Pairs → InlinedResults, for clarity
- Restructure docs into goal-axis taxonomy with per-package contracts

### Fixed
- Drop thinking-only assistant messages before sending to LLM
- /agent and /skills tab switching skips empty tabs despite help hint (zhujian)
- Preserve old agent IDs across ResyncMessages reconciliation
- Skip interrupt marker in ExtractLastUserText
- Wake Update loop on background hub events
- Remove dead autoTheme variable from theme init (zhujian)

## [v1.18.0] - 2026-05-17

### Added
- Add native DeepSeek provider with updated model catalog and V4 readiness checks (zhujian)
- Add Claude model catalog updates including 1M context support (zhujian)
- Add trace recorder for inference, system, tools, and content provenance (Meng Yan)
- Add web viewer for session tracing and inspection (Meng Yan)

### Changed
- Rename trace concepts to inspector and update related system prompts (Meng Yan)
- Unify record/payload naming and append-only transcript persistence with fsync batching (Meng Yan)
- Refine README feature, usage, configuration, skills, extensions, and open-architecture documentation (Meng Yan)

### Fixed
- Canonicalize `/model` command usage and remove the `/provider` alias (zhujian, Meng Yan)
- Fix stable message IDs, unconditional state patches, and early `session.started` telemetry writes (Meng Yan)
- Escape session IDs in the viewer and deduplicate SSE records across reconnects (Meng Yan)
- Address DeepSeek provider review feedback (zhujian)

## [v1.17.4] - 2026-05-06

### Changed
- Simplify input queue to single-source-of-truth FIFO model, removing SentToInbox/WaitingCount tracking and the "waiting" badge

## [v1.17.3] - 2026-05-06

### Added
- Tavily search provider

### Changed
- Rename BigModel provider display to Z.ai (GLM series)

### Fixed
- Restore Exa search provider after MCP endpoint changes

## [v1.17.2] - 2026-05-06

### Added
- BigModel (Zhipu GLM) LLM provider

### Changed
- Add queue depth metrics and improve queue processing

## [v1.17.1] - 2026-05-05

### Added
- Manual feature documentation for v1.17

### Changed
- Remove dead code and modernize Go patterns

## [v1.17.0] - 2026-05-04

### Added
- Reminder system for proactive context injection during agent execution

### Changed
- Streamlined extensibility documentation in README
- Updated benchmark documentation title
- Updated CHANGELOG with latest changes

## [v1.16.0] - 2026-05-04

### Added
- Open Identity: configurable assistant personas as markdown files at user or project scope; switch with `/identity`. Built-in `identity create` / `identity edit` workflows and auto-generated user-level template.
- Structured system prompt catalog: layered Slot/Section model with hot-patching (`Use` / `Drop` / `Refresh`).
- Reusable panel rendering for input-view selectors.

### Changed
- System prompt assembly refactored around `Section` and `Scope` types; subagent identity is replaced rather than overlaid.
- Documentation reorganized; new `docs/system-prompt.md` consolidates prompt design.

### Removed
- Agent fork mode (`Agent(fork: true)`) — subagents always start with fresh context.
- Legacy prompt template files (`base.txt`, `tools-*.txt`); replaced by `prompts/identity.txt`, `prompts/policy.txt`, `prompts/guidelines/*.txt`.

## [v1.15.14] - 2026-05-02

### Fixed
- Operation mode indicator icon and hint text.

## [v1.15.13] - 2026-05-02

### Removed
- Obsolete permission documentation.

## [v1.15.12] - 2026-05-02

### Added
- Permission system with mode-based access control for agents and tools.
- Subagent matching and routing logic.
- Permission docs (`docs/claude-permission.md`, `docs/gen-permission.md`).

### Changed
- Subagent executor / loader / registry refactored for type safety.
- Improved bash AST parsing and settings merger.

## [v1.15.11] - 2026-05-01

### Added
- Permission modes for agent execution: `explore`, `edit`, `default`.
- Agent name display logic with generic vs. custom name handling.

### Changed
- Renamed `continueagent` to `continuation`; removed deferred tool.
- Improved progress tracking and queue preview UX.

## [v1.15.10] - 2026-05-01

### Fixed
- Test signatures aligned with updated `renderTask` and queue preview design.

## [v1.15.9] - 2026-05-01

### Added
- Queue methods `DequeuePending` and `RemoveSentToInbox` for precise sent-item lifecycle.
- `HandleAgentMessage` for processing agent-injected user messages.

### Fixed
- Queue input injection: properly remove injected queued items and hold turn boundary until agent confirms.

## [v1.15.8] - 2026-04-30

### Added
- Queue selection: `Up` / `Down` navigate between queue items and history entries.
- OpenAI model token limits fetched from official docs with caching.

### Changed
- Tool execution: parallel only for read-only batches; sequential when side effects are possible.
- Edit tool: clearer error messages when `old_string` is missing or non-unique.
- System prompts: clarify that dependent tool calls must not be batched.
- Queue selected-item styling.

### Fixed
- Release workflow: full git history checkout for CHANGELOG section parsing.

## [v1.15.7] - 2026-04-30

### Changed
- Bind thinking effort to `Ctrl+T`.

### Fixed
- Conversation message handling.

## [v1.15.6] - 2026-04-29

### Fixed
- Min / max item constraints in `AskUserQuestion` schemas.

### Changed
- Release metadata.

## [v1.15.5] - 2026-04-26

### Removed
- Timer model render.

## [v1.15.4] - 2026-04-25

### Added
- MiniMax LLM provider (M2.x family, including Highspeed variants).

### Changed
- README updated with MiniMax provider information.

## [v1.15.3] - 2026-04-25

### Changed
- Refactored Anthropic and OpenAI clients with catalog support.
- Added catalog tests for Anthropic and OpenAI providers.

### Removed
- Thinking-level handling and related model configuration.

### Fixed
- Vertex AI integration for Anthropic models.

## [v1.15.2] - 2026-04-24

### Changed
- CI: use the current changelog section as release notes.
- Build: add `release-push` make target.

### Fixed
- v1.15.1 release notes show only the current version section.

## [v1.15.1] - 2026-04-24

### Fixed
- Hide queue badges and preview entries for items already sent.
- Keep queue selection focused on the last pending item; exit selection when no longer pending.
- Preserve assistant tool-call rendering while tool results are still arriving.
- Summarize repeated tool calls instead of duplicating output.
- Attach `CHANGELOG.md` to GitHub release artifacts.

## [v1.15.0] - 2026-04-24

### Added
- MiniMax provider (initial integration: API key, catalog, client).
- LLM cost tracking via `Money` and `Cost` types.
- Per-message cost tracking in conversations.
- Provider selection and model enrichment.

### Fixed
- API compatibility error handling.
