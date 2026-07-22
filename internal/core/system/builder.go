// Package system constructs and manages the layered system prompt for an
// agent. See internal/core/section.go for the Slot layout and Section type.
//
// Construction is via Build(scope, opts...). The prompt is assembled from a
// small fixed set of parts — identity, behavior, rules, environment — each
// rendered once from the resolved buildConfig. Options populate that config;
// they do not mutate the System directly.
package system

import (
	"github.com/genai-io/san/internal/core"
)

// Option configures a buildConfig during Build. Options are applied in order.
type Option func(*buildConfig)

// Persona holds the prompt-part overrides a persona supplies — identity,
// behavior, and rules. An empty field means "keep San's built-in default for
// that part". This is the system layer's view of a persona (just the prompt
// bodies); the full on-disk bundle lives in internal/persona.Persona.
type Persona struct {
	Identity string
	Behavior string
	Rules    string
}

// buildConfig accumulates everything Build needs to render the prompt parts.
// Options populate it; Build then constructs each part once from the resolved
// values.
type buildConfig struct {
	scope    core.Scope
	env      *Environment   // volatile footer; nil when not supplied
	persona  Persona        // per-part overrides; empty fields use the default
	subagent *SubagentBrief // non-nil for a subagent charter
}

// Build constructs a System for the given Scope and applies the options.
//
// The prompt is four parts in slot order: identity (who you are), behavior
// (how you work — main agent only), rules (safety + protocols, scope-aware),
// and environment (volatile footer). Each part is a single named section, so
// a persona can later replace a whole part by name with one file.
func Build(scope core.Scope, opts ...Option) core.System {
	cfg := &buildConfig{scope: scope}
	for _, opt := range opts {
		opt(cfg)
	}

	sys := core.NewSystem()
	const caller = "system:init"

	// Identity (slot 0): subagent charter, persona override, or default.
	if cfg.subagent != nil {
		sys.Use(subagentIdentitySection(*cfg.subagent), caller)
	} else {
		sys.Use(identitySection(cfg.persona.Identity), caller)
	}

	// Behavior (slot 1): communication style + engineering defaults. Main
	// agent only — subagents carry their working style in their charter.
	if scope == core.ScopeMain {
		sys.Use(behaviorSection(cfg.persona.Behavior), caller)
	}

	// Rules (slot 2): safety contract + harness protocols. Per-tool guidance
	// lives in tool schemas, so the rules part does not vary by scope or tool
	// availability.
	sys.Use(rulesSection(cfg.persona.Rules), caller)

	// Environment (slot 3): volatile footer, only when supplied.
	if cfg.env != nil {
		sys.Use(environmentSection(*cfg.env), caller)
	}

	return sys
}

// CompactPrompt returns the standalone prompt for the conversation compactor.
// Compaction is a one-shot LLM call, not a long-lived System.
func CompactPrompt() string { return cachedCompact }
