package core

// System manages the composable, mutable system prompt.
//
// The system prompt defines WHO the agent is, WHAT it knows, and HOW it
// behaves. It is built from a set of Sections, each owning one Slot in the
// prompt layout (see Slot for the order).
//
// Lifecycle:
//   - Construct via internal/core/system.Build, which Use's stock sections.
//   - At runtime, app/subagent code may Use, Drop, or Refresh sections to
//     reflect state changes (skill activation, cwd switch, hook injection).
//   - For subagents, the System dies with the agent — no explicit cleanup.
//
// Concurrency: implementations must be safe for concurrent use; the agent
// loop reads Prompt() while hooks/UI may mutate sections.
type System interface {
	// Prompt returns the assembled system prompt. Result is cached and
	// invalidated only when sections change.
	Prompt() string

	// Use registers or replaces a section by Name. Caller is a short tag
	// describing what triggered the mutation (e.g. "system:init",
	// "command:/identity") and surfaces in the trace.
	Use(sec Section, caller string)

	// Drop removes a section by Name. No-op if absent.
	Drop(name, caller string)

	// Refresh marks one section's rendered output stale. Use after the
	// section's underlying state changed but the Section value did not.
	Refresh(name, caller string)

	// Sections returns a snapshot of currently registered sections in
	// render order (slot ascending, insertion order ascending). Used by
	// observers that attach after construction to replay the existing state.
	Sections() []Section

	// SetObserver installs a callback invoked synchronously on every
	// subsequent mutation. Attaching also replays existing sections as
	// "added" events so the observer sees a complete history starting
	// from the moment of attachment.
	SetObserver(fn func(SystemChange))
}
