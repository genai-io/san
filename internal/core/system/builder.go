// Package system constructs and manages the layered system prompt for an
// agent. See internal/core/section.go for the Slot layout and Section type.
//
// Construction is via Build(scope, opts...). The prompt is assembled from a
// small fixed set of parts — identity, behavior, rules, environment — each
// rendered once from the resolved buildConfig. Options populate that config;
// they do not mutate the System directly, so a part may depend on several
// options at once (e.g. rules needs both isGit and provider).
package system

import (
	"github.com/genai-io/san/internal/core"
)

// Option configures a buildConfig during Build. Options are applied in order.
type Option func(*buildConfig)

// buildConfig accumulates everything Build needs to render the prompt parts.
// Options populate it; Build then constructs each part once from the resolved
// values.
type buildConfig struct {
	scope    core.Scope
	isGit    bool
	provider string
	env      *Environment   // volatile footer; nil when not supplied
	identity string         // persona/user identity override; "" = built-in default
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

	// Identity (slot 0): subagent charter, persona/user override, or default.
	if cfg.subagent != nil {
		sys.Use(subagentIdentitySection(*cfg.subagent), caller)
	} else {
		sys.Use(identitySection(cfg.identity), caller)
	}

	// Behavior (slot 1): communication style + engineering defaults. Main
	// agent only — subagents carry their working style in their charter.
	if scope == core.ScopeMain {
		sys.Use(behaviorSection(), caller)
	}

	// Rules (slot 2): safety contract + tool/task/git protocols, scope-aware,
	// with git folded in when isGit and any provider quirks appended.
	sys.Use(rulesSection(scope, cfg.isGit, cfg.provider), caller)

	// Environment (slot 3): volatile footer, only when supplied.
	if cfg.env != nil {
		sys.Use(environmentSection(*cfg.env), caller)
	}

	return sys
}

// CompactPrompt returns the standalone prompt for the conversation compactor.
// Compaction is a one-shot LLM call, not a long-lived System.
func CompactPrompt() string { return cachedCompact }
