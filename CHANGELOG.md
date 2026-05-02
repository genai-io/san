# Changelog

All notable changes to this project will be documented in this file.

## [v0.0.2] - 2026-05-02

### Fixed

- Fix(internal/app/conv): commit the message conversion logic

## [v0.0.1] - 2026-05-02

### Fixed

- Fix(ui): improve operation mode indicator icon and hint text

## [v1.15.13] - 2026-05-02

### Changed

- Remove the permission doc

## [v1.15.12] - 2026-05-02

### Added

- Permission system with mode-based access control for agents and tools
- Subagent matching and routing logic
- Permission documentation (`docs/claude-permission.md`, `docs/gen-permission.md`)

### Changed

- Refactor subagent executor, loader, and registry for improved type safety
- Improve bash AST parsing and setting merger

## [v1.15.11] - 2026-05-01

### Added

- Permission mode support for agent execution (explore, edit, default)
- Agent name display logic with generic vs. custom name handling
- Comprehensive tests for new agent behaviors

### Changed

- Refactor agent input parsing in tool rendering for cleaner code
- Refactor: rename `continueagent` to `continuation` and remove deferred tool
- Improve progress tracking and queue preview UX

## [v1.15.10] - 2026-05-01

### Fixed

- Fix `renderTask` test to match updated 4-parameter signature
- Update `TestRenderQueuePreviewShowsWaitingItems` to align with queue preview design

## [v1.15.9] - 2026-05-01

### Fixed

- Queue input injection: properly remove injected queued items and hold turn boundary until agent confirms

### Added

- `DequeuePending` and `RemoveSentToInbox` queue methods for precise sent-item lifecycle
- `HandleAgentMessage` to process agent-injected user messages and sync queue state

## [v1.15.8] - 2026-04-30

### Added

- Queue selection: Up/Down keys now navigate between queue items and history entries seamlessly

### Added

- Fetch OpenAI model token limits from official developer docs with caching

### Changed

- Tool execution: parallel only for read-only batches; sequential when side effects are possible
- Better edit tool error messages when old_string not found or not unique
- System prompts: clarify dependent tool calls must not be batched
- Update queue selected item styling with background highlight

### Fixed

- Checkout full history to find CHANGELOG sections in release workflow

## [v1.15.7] - 2026-04-30

### Fixed

- Update conversation message handling

### Changed

- Bind effort with keyboard ctrl+t

## [v1.15.6] - 2026-04-29

### Changed

- Update release metadata

### Fixed

- Add min and max item constraints to ask-user-question schemas

## [v1.15.5] - 2026-04-26

### Changed

- Remove the timer model render

## [v1.15.4] - 2026-04-25

### Added

- **MiniMax LLM Provider**: Add MiniMax provider with M2.7, M2.7 Highspeed, M2.5, M2.5 Highspeed, M2.1, M2.1 Highspeed, M2 models

### Changed

- Update README with MiniMax provider information

## [v1.15.3] - 2026-04-25

### Changed

- Remove thinking-level handling and related model configuration
- Refactor Anthropic and OpenAI client implementations with improved catalog support
- Add catalog tests for Anthropic and OpenAI providers

### Fixed

- Correct Vertex AI integration for Anthropic models

## [v1.15.2] - 2026-04-24

### Changed

- CI: Use current changelog section as release notes instead of full changelog
- Build: Add `release-push` make target to streamline version publishing

### Fixed

- Correct v1.15.1 release notes to show only current version section

## [v1.15.1] - 2026-04-24

### Fixed

- Hide queue badges and queue preview entries for items already sent to inbox
- Keep queue selection focused on the last pending item and exit selection if an item is no longer pending
- Preserve assistant tool-call rendering while tool results are still arriving
- Summarize repeated tool calls in conversation text instead of printing duplicate lines
- Attach `CHANGELOG.md` to GitHub release artifacts

### Tests

- Add coverage for pending queue filtering, hidden queue badge behavior, and aggregated tool-call text output

[Full Changelog](https://github.com/genai-io/gen-code/compare/v1.15.0...v1.15.1)

## [v1.15.0] - 2026-04-24

### Added

- **MiniMax LLM Provider**: Add MiniMax provider implementation with API key, catalog, and client support
- **Cost Tracking**: Add cost calculation for LLM usage with Money and Cost types
- **Conversation Cost Tracking**: Add cost tracking to conversation messages
- **Provider Selection**: Add provider selection and model enrichment

### Fixed

- **API Compatibility**: Fix API compatibility error handling

### Tests

- Add tests for cost estimation and provider selection

[Full Changelog](https://github.com/genai-io/gen-code/compare/v1.14.9...v1.15.0)
