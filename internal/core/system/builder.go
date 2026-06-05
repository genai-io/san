// Package system constructs and manages the layered system prompt for an
// agent. See internal/core/section.go for the Slot layout and Section type.
//
// Construction is via Build(scope, opts...). Stock sections (identity, policy,
// guidelines) apply automatically based on Scope; runtime-varying content is
// added through option functions (WithMemory, WithSkills, ...).
package system

import (
	"github.com/genai-io/san/v2/internal/core"
)

// Option configures a System during Build. Options are applied in order;
// later options can override earlier ones by reusing the same Section Name.
type Option func(core.System, core.Scope)

// Build constructs a System for the given Scope and applies the options.
//
// Always-on defaults (identity, policy, common guidelines) are registered
// first so option-supplied sections may override them by Name. ScopeSubagent
// omits task and question guidelines by default, since those tools are
// main-only.
func Build(scope core.Scope, opts ...Option) core.System {
	sys := core.NewSystem()
	applyDefaults(sys, scope)
	for _, opt := range opts {
		opt(sys, scope)
	}
	return sys
}

// CompactPrompt returns the standalone prompt for the conversation compactor.
// Compaction is a one-shot LLM call, not a long-lived System.
func CompactPrompt() string { return cachedCompact }
