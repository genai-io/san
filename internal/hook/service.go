// Package hook executes user-defined hooks at named application events
// (PreToolUse, Stop, SessionStart, PermissionRequest, …) and merges
// their structured outcomes back into the calling code path.
//
// The package exposes one role interface plus the concrete engine:
//
//   - Handler — fire hooks for an application event, query whether any
//     are configured. Implemented by *Engine. Consumers: agent loop,
//     slash command approvals, compaction, file watcher, worktree /
//     plugin / mcp / subagent. Each takes Handler instead of *Engine
//     to express that it only fires events, never configures the
//     engine. Modeled on http.Handler — same fire-and-return shape.
//   - *Engine — the only implementation. Carries the additional Set*
//     and ClearSessionHooks methods used by the app composition root
//     to wire callbacks and reload context, plus CurrentStatusMessage
//     for the TUI status line. Each of these surfaces has a single
//     caller; an interface would be ceremony.
package hook

import (
	"context"

	"github.com/genai-io/san/v2/internal/setting"
)

// Handler is what callers depend on to fire hooks at application
// events and merge outcomes. Modeled on http.Handler: pass an event
// (the request analogue) and receive the merged HookOutcome (the
// response analogue).
//
// Reading a call site:
//
//	handler.Execute(ctx, hook.PreToolUse, input)
//	// "the hook handler executes the PreToolUse event"
//
// Most callers depend on this surface only. The app composition root
// and the TUI view consume the additional methods on *Engine directly.
type Handler interface {
	Execute(ctx context.Context, event EventType, input HookInput) HookOutcome
	ExecuteAsync(event EventType, input HookInput)
	HasHooks(event EventType) bool
	StopHookActive() *bool
}

// Compile-time guarantee that *Engine satisfies Handler.
var _ Handler = (*Engine)(nil)

// Options holds the dependencies needed to create the default hook engine.
// All fields must be supplied by the caller — the hook package does not reach into
// global singletons.
type Options struct {
	Settings       *setting.Data
	SessionID      string
	CWD            string
	TranscriptPath string
	Completer      LLMCompleter
	ModelID        string
	EnvProvider    func(context.Context) []string
}

// Initialize creates the singleton hook engine from the given options.
func Initialize(opts Options) {
	e := NewEngine(opts.Settings, opts.SessionID, opts.CWD, opts.TranscriptPath)
	e.SetLLMCompleter(opts.Completer, opts.ModelID)
	if opts.EnvProvider != nil {
		e.SetEnvProvider(opts.EnvProvider)
	}
	defaultEngine = e
}

// DefaultEngine returns the package-level *Engine. Returns the
// pre-Initialize zero-hook engine if Initialize has not run yet.
//
// This is the only seam for callers that need the full *Engine surface
// (the app composition root). All other consumers should depend on
// Dispatcher (or Status) instead.
func DefaultEngine() *Engine {
	return defaultEngine
}

// SetDefaultEngine replaces the package-level engine. Intended for
// tests. A nil argument restores the empty pre-Initialize engine.
func SetDefaultEngine(e *Engine) {
	if e == nil {
		defaultEngine = newEmptyEngine()
		return
	}
	defaultEngine = e
}

// ResetDefaultEngine restores the empty pre-Initialize engine.
// Intended for tests.
func ResetDefaultEngine() {
	defaultEngine = newEmptyEngine()
}

// defaultEngine is the package-level hook engine. Initialized to an
// empty (zero-hook) engine so callers that fire events before
// Initialize is called don't crash.
var defaultEngine = newEmptyEngine()

// newEmptyEngine returns an Engine wired to empty settings — fires no
// hooks until Initialize installs a real one.
func newEmptyEngine() *Engine {
	return NewEngine(setting.NewData(), "", "", "")
}
