package core

// Slot is a stable position in the system prompt. Sections render in
// ascending Slot order; ties break by insertion order so callers can
// control fine-grained order within a slot (e.g. user memory before
// project memory).
//
// Keep stable instructions before the volatile environment. Cache breakpoints
// depend on the provider's wire format.
type Slot uint8

const (
	SlotIdentity    Slot = iota // who you are (replaceable per persona / subagent charter)
	SlotBehavior                // how you communicate and work (style + engineering)
	SlotRules                   // safety contract + tool / task / git protocols
	SlotEnvironment             // cwd, date, platform, shell
)

// Source labels where a section originated, for debugging and provenance.
type Source string

const (
	Predefined Source = "predefined" // embedded templates
	FromFile   Source = "file"       // AGENTS.md, agent definitions, skill defs
	Injected   Source = "injected"   // passed by parent agent or app layer
	Dynamic    Source = "dynamic"    // generated at runtime (env, hook context)
)

// Scope distinguishes which kind of agent a System is being built for.
// Used by Build to gate which default sections apply (e.g. ScopeSubagent
// skips task/question guidelines).
//
// Note: distinct from message.Role (user/assistant/tool); message.Role names
// who produced a message, Scope names who the prompt is for.
type Scope uint8

const (
	ScopeMain     Scope = iota // top-level interactive agent
	ScopeSubagent              // spawned by Agent tool; leaf node by default
)

// Section is one composable piece of the system prompt.
//
// Render is a pure function: it pulls current state and returns rendered text
// (typically wrapped in an XML tag). Returning "" skips the section entirely.
//
// Each section has a stable Name used for Use/Drop/Refresh. Render output is
// cached per section; Refresh forces re-evaluation on the next Prompt() call.
type Section struct {
	Slot   Slot
	Name   string        // stable id, e.g. "memory-user", "capabilities-skills"
	Source Source        // origin tag, debugging only
	Render func() string // pure renderer; "" means skip
}
