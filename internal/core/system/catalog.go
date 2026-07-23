package system

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/genai-io/san/internal/core"
)

// Embedded prompt templates. One file per system-prompt part, mirroring the
// four-part structure — a persona overrides a part by dropping in the same-
// named file under its system/ directory:
//
//	prompts/identity.txt              — who you are (the "identity" part)
//	prompts/behavior.txt              — how you work (the "behavior" part)
//	prompts/rules.txt                 — safety + system reminders (the "rules" part)
//	prompts/compact.txt               — conversation compactor prompt (standalone)
//
// These compose into four parts, top to bottom:
//
//	You are a coding agent, …         (identity, raw preamble)
//	<behavior> … </behavior>          (main agent only)
//	<rules> … </rules>
//	<environment> … </environment>    (volatile footer)
//
// Identity is bare because Anthropic's standard preamble shape starts with
// "You are X". The other parts live in a named XML envelope so the model can
// address each as a structured unit, and so a persona can replace a whole
// part by dropping in one file (system/<part>.md).
//
// Per-tool guidance (when to reach for a tool, how to call it) lives in each
// tool's schema description, not here — the prompt carries only cross-tool
// rules a single description cannot express, and a disabled tool takes its
// guidance with it because the schema disappears from the request.
//
//go:embed prompts/*.txt
var promptFS embed.FS

// init-time read of every static template. Keeps Build() allocation-light.
var (
	cachedIdentity = loadEmbed("prompts/identity.txt")
	cachedBehavior = loadEmbed("prompts/behavior.txt")
	cachedRules    = loadEmbed("prompts/rules.txt")
	cachedCompact  = loadEmbed("prompts/compact.txt")
)

// loadEmbed reads a required embedded prompt and trims surrounding whitespace.
// Embedded files are bundled at build time, so a missing path is a programmer
// error and panics rather than silently producing an empty section.
func loadEmbed(path string) string {
	data, err := promptFS.ReadFile(path)
	if err != nil {
		panic("system: missing embedded prompt " + path + ": " + err.Error())
	}
	return strings.TrimSpace(string(data))
}

// XML envelope

// wrap returns body enclosed in <name attr="...">...</name>. Empty body
// (after trimming) yields "" so callers can short-circuit by Render returning "".
func wrap(name string, attrs map[string]string, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(name)
	for _, k := range sortedKeys(attrs) {
		fmt.Fprintf(&b, " %s=%q", k, attrs[k])
	}
	b.WriteString(">\n")
	b.WriteString(body)
	b.WriteString("\n</")
	b.WriteString(name)
	b.WriteByte('>')
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Part: identity (slot 0)

// identitySection renders the "who you are" preamble. A non-empty override
// (a persona's identity part) replaces the built-in default. Rendered raw
// (no XML envelope) to match Anthropic's standard "You are X" preamble.
func identitySection(override string) core.Section {
	body := strings.TrimSpace(override)
	source := core.Predefined
	if body == "" {
		body = cachedIdentity
	} else {
		source = core.FromFile
	}
	return core.Section{
		Slot: core.SlotIdentity, Name: "identity", Source: source,
		Render: func() string { return body },
	}
}

// Part: behavior (slot 1)

// behaviorSection renders how the agent communicates and works — the merge of
// the communication style (Tone / Updates / Behavior) and the engineering
// defaults (Restraint / Code conventions / Error handling). Main-agent only;
// subagents carry their working style in their charter.
func behaviorSection(override string) core.Section {
	override = strings.TrimSpace(override)
	source := core.Predefined
	if override != "" {
		source = core.FromFile
	}
	return core.Section{
		Slot: core.SlotBehavior, Name: "behavior", Source: source,
		Render: func() string {
			body := override
			if body == "" {
				body = cachedBehavior
			}
			return wrap("behavior", nil, body)
		},
	}
}

// Part: rules (slot 2)

// rulesSection renders the safety contract and harness protocols. A non-empty
// override (a persona's rules part) replaces the built-in default.
func rulesSection(override string) core.Section {
	override = strings.TrimSpace(override)
	source := core.Predefined
	if override != "" {
		source = core.FromFile
	}
	return core.Section{
		Slot: core.SlotRules, Name: "rules", Source: source,
		Render: func() string {
			body := override
			if body == "" {
				body = cachedRules
			}
			return wrap("rules", nil, body)
		},
	}
}

// Options

// WithPersona overrides any of the identity / behavior / rules parts at build
// time from a persona. Empty fields keep San's built-in default for that part.
// Applied wholesale, so pass every part the persona provides.
func WithPersona(p Persona) Option {
	return func(cfg *buildConfig) { cfg.persona = p }
}

// SwapPersona replaces the identity / behavior / rules parts on an already-built
// main-agent system — e.g. a mid-session persona switch. Empty fields revert
// that part to the built-in default. Visible on the next sys.Prompt().
func SwapPersona(sys core.System, p Persona) {
	const caller = "command:persona"
	sys.Use(identitySection(p.Identity), caller)
	sys.Use(behaviorSection(p.Behavior), caller)
	sys.Use(rulesSection(p.Rules), caller)
}

// Subagent identity (Scope == ScopeSubagent)

// SubagentBrief carries everything needed to render a subagent's identity.
// It is set once at subagent creation and never mutated; the brief lives only
// as long as the subagent's core.System (one ThinkAct cycle).
//
// Tools are not listed here — the LLM sees them via the schema list. Only
// pattern-level constraints (which are invisible in the schema) need surfacing.
type SubagentBrief struct {
	AgentName       string   // fixed to "subagent" for worker runs
	Description     string   // one-line role description
	Mode            string   // effective runtime policy (model requests only explore/edit/default)
	ToolConstraints []string // e.g. "Bash limited to git diff*"
}

// WithSubagentIdentity replaces the default identity with a subagent charter.
// Mode and tool constraints are folded in here, so subagents have no separate
// "assignment" section to consult — identity carries the whole job.
func WithSubagentIdentity(b SubagentBrief) Option {
	return func(cfg *buildConfig) { brief := b; cfg.subagent = &brief }
}

func subagentIdentitySection(b SubagentBrief) core.Section {
	return core.Section{
		Slot: core.SlotIdentity, Name: "identity", Source: core.Injected,
		Render: func() string { return renderSubagentIdentity(b) },
	}
}

func renderSubagentIdentity(b SubagentBrief) string {
	var sb strings.Builder
	if b.AgentName == "" || b.AgentName == "subagent" {
		sb.WriteString("You are a subagent.\n")
	} else {
		fmt.Fprintf(&sb, "You are a %s subagent.\n", b.AgentName)
	}
	if b.Description != "" {
		fmt.Fprintf(&sb, "Role: %s\n", b.Description)
	}
	if b.Mode != "" || len(b.ToolConstraints) > 0 {
		sb.WriteByte('\n')
	}
	if b.Mode != "" {
		fmt.Fprintf(&sb, "Operational scope: %s.\n", modeDescription(b.Mode))
	}
	if len(b.ToolConstraints) > 0 {
		fmt.Fprintf(&sb, "Tool constraints: %s.\n", strings.Join(b.ToolConstraints, "; "))
	}
	attrs := map[string]string{}
	if b.Mode != "" {
		attrs["mode"] = b.Mode
	}
	return wrap("identity", attrs, sb.String())
}

func modeDescription(mode string) string {
	switch mode {
	case "explore":
		return "read-only research; do not modify files; shell commands are limited to operations classified as read-only"
	case "acceptEdits":
		return "may read and edit files; other gated tools are denied automatically"
	case "bypass", "bypassPermissions":
		return "permission checks bypassed; act with care on destructive operations"
	default:
		return "read and analysis tools only; operations that require approval are denied automatically"
	}
}

// Part: environment (slot 3, volatile)

// Environment is the small, frequently-changing footer: cwd, git branch,
// platform, today's date. Placed last so the cache prefix above it survives
// daily date rollovers and cwd switches.
type Environment struct {
	Cwd string
}

// WithEnvironment registers the environment section. Callers should refresh
// it via sys.Refresh("environment") when cwd changes mid-session.
func WithEnvironment(env Environment) Option {
	return func(cfg *buildConfig) {
		e := env
		cfg.env = &e
	}
}

func environmentSection(env Environment) core.Section {
	return core.Section{
		Slot: core.SlotEnvironment, Name: "environment", Source: core.Dynamic,
		Render: func() string { return renderEnvironment(env) },
	}
}

func renderEnvironment(env Environment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "date: %s\ncwd: %s", time.Now().Format("2006-01-02"), env.Cwd)
	if branch := gitBranch(env.Cwd); branch != "" {
		fmt.Fprintf(&b, "\nbranch: %s", branch)
	}
	fmt.Fprintf(&b, "\nplatform: %s/%s", runtime.GOOS, runtime.GOARCH)
	return wrap("environment", nil, b.String())
}

// gitBranch resolves the current branch name by reading .git/HEAD directly —
// no subprocess. It walks up from cwd to find the repo root and follows the
// "gitdir:" indirection a linked worktree's .git file carries. Returns "" when
// nothing readable is found; a detached HEAD yields the short commit hash.
func gitBranch(cwd string) string {
	dir := cwd
	for {
		gitPath := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitPath); err == nil {
			return branchFromGitPath(gitPath, info.IsDir())
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func branchFromGitPath(gitPath string, isDir bool) string {
	gitDir := gitPath
	if !isDir {
		data, err := os.ReadFile(gitPath)
		if err != nil {
			return ""
		}
		gitDir = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(head))
	if branch, ok := strings.CutPrefix(ref, "ref: refs/heads/"); ok {
		return branch
	}
	if len(ref) >= 7 {
		return ref[:7] + " (detached)"
	}
	return ""
}
