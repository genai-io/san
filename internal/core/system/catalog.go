package system

import (
	"embed"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/genai-io/gen-code/internal/core"
)

// Embedded prompt templates. Layout:
//
//	prompts/identity.txt              — default agent persona
//	prompts/policy.txt                — safety contract (never overridden)
//	prompts/compact.txt               — conversation compactor prompt
//	prompts/guidelines/{tools,git,questions,tasks}.txt
//	prompts/providers/<name>.txt      — provider-specific quirks (optional)
//
//go:embed prompts/*.txt prompts/guidelines/*.txt
var promptFS embed.FS

// init-time read of every static template. Keeps Build() allocation-free.
var (
	cachedIdentity  = loadEmbed("prompts/identity.txt")
	cachedPolicy    = loadEmbed("prompts/policy.txt")
	cachedCompact   = loadEmbed("prompts/compact.txt")
	cachedTools     = loadEmbed("prompts/guidelines/tools.txt")
	cachedGit       = loadEmbed("prompts/guidelines/git.txt")
	cachedQuestions = loadEmbed("prompts/guidelines/questions.txt")
	cachedTasks     = loadEmbed("prompts/guidelines/tasks.txt")
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

// loadEmbedOptional is like loadEmbed but returns "" for missing files.
// Used for optional templates (e.g. provider-specific quirks).
func loadEmbedOptional(path string) string {
	data, err := promptFS.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// applyDefaults registers the always-on sections for a Scope.
// Options passed to Build can override identity by Name.
func applyDefaults(sys core.System, scope core.Scope) {
	const caller = "system:init"
	sys.Use(defaultIdentity(), caller)
	sys.Use(policy(), caller)
	sys.Use(guidelines("tools", cachedTools), caller)
	if scope == core.ScopeMain {
		// Task tracking + interactive questions are main-agent behaviors.
		sys.Use(guidelines("tasks", cachedTasks), caller)
		sys.Use(guidelines("questions", cachedQuestions), caller)
	}
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

// Default sections (auto-applied)

func defaultIdentity() core.Section {
	// Identity is rendered raw (no XML envelope) so the model sees a clean
	// "You are X" opening, matching Anthropic's standard preamble shape.
	return core.Section{
		Slot: core.SlotIdentity, Name: "identity", Source: core.Predefined,
		Render: func() string { return cachedIdentity },
	}
}

func policy() core.Section {
	return core.Section{
		Slot: core.SlotPolicy, Name: "policy", Source: core.Predefined,
		Render: func() string { return wrap("policy", nil, cachedPolicy) },
	}
}

func guidelines(name, body string) core.Section {
	return core.Section{
		Slot: core.SlotGuidelines, Name: "guidelines-" + name, Source: core.Predefined,
		Render: func() string {
			return wrap("guidelines", map[string]string{"name": name}, body)
		},
	}
}

// Options

// WithIdentity replaces the default identity section, e.g. a user-defined
// "ML engineer" persona. Pass an empty string to keep the default.
func WithIdentity(text string) Option {
	return func(sys core.System, _ core.Scope) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		sys.Use(identitySection(text), "system:init")
	}
}

// SwapIdentity replaces the identity slot on an already-built system.
// Empty text reverts to the built-in default. Visible on the next sys.Prompt().
func SwapIdentity(sys core.System, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		sys.Use(defaultIdentity(), "command:identity")
		return
	}
	sys.Use(identitySection(text), "command:identity")
}

// identitySection builds the slot-0 identity Section for a user-defined persona.
func identitySection(text string) core.Section {
	return core.Section{
		Slot: core.SlotIdentity, Name: "identity", Source: core.FromFile,
		Render: func() string { return text },
	}
}

// WithProvider injects provider-specific quirks if a matching template exists
// at prompts/providers/<name>.txt. Missing files are silently skipped.
func WithProvider(name string) Option {
	return func(sys core.System, _ core.Scope) {
		if name == "" {
			return
		}
		body := loadEmbedOptional("prompts/providers/" + name + ".txt")
		if body == "" {
			return
		}
		sys.Use(core.Section{
			Slot: core.SlotProvider, Name: "provider", Source: core.Predefined,
			Render: func() string {
				return wrap("provider", map[string]string{"name": name}, body)
			},
		}, "system:init")
	}
}

// WithGitGuidelines toggles the git safety guidelines. Off by default.
func WithGitGuidelines(isGit bool) Option {
	return func(sys core.System, _ core.Scope) {
		if !isGit {
			return
		}
		sys.Use(guidelines("git", cachedGit), "system:init")
	}
}

// Subagent identity (Scope == ScopeSubagent)

// SubagentBrief carries everything needed to render a subagent's identity.
// It is set once at subagent creation and never mutated; the brief lives only
// as long as the subagent's core.System (one ThinkAct cycle).
//
// Tools are not listed here — the LLM sees them via the schema list. Only
// pattern-level constraints (which are invisible in the schema) need surfacing.
type SubagentBrief struct {
	AgentName       string   // e.g. "code-reviewer"
	Description     string   // one-line role description
	Mode            string   // "explore" / "default" / "acceptEdits" / "bypass"
	ToolConstraints []string // e.g. "Bash limited to git diff*"
	CustomPrompt    string   // AGENT.md body
}

// WithSubagentIdentity replaces the default identity with a subagent charter.
// Mode and tool constraints are folded in here, so subagents have no separate
// "assignment" section to consult — identity carries the whole job.
func WithSubagentIdentity(b SubagentBrief) Option {
	return func(sys core.System, _ core.Scope) {
		sys.Use(core.Section{
			Slot: core.SlotIdentity, Name: "identity", Source: core.Injected,
			Render: func() string { return renderSubagentIdentity(b) },
		}, "subagent:init")
	}
}

func renderSubagentIdentity(b SubagentBrief) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "You are a %s subagent operating inside Gen Code.\n", b.AgentName)
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
	if body := strings.TrimSpace(b.CustomPrompt); body != "" {
		sb.WriteString("\n")
		sb.WriteString(body)
		sb.WriteByte('\n')
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
		return "read-only research; do not modify files or run shell commands"
	case "acceptEdits":
		return "may read and edit files; gated tools require approval"
	case "bypass":
		return "permission checks bypassed; act with care on destructive operations"
	default:
		return "default permissions; gated tools prompt for approval"
	}
}

// Environment (volatile, sits at the end of the prompt)

// Environment is the small, frequently-changing footer: cwd, git, platform,
// model, today's date. Placed last so the cache prefix above it survives
// daily date rollovers and cwd switches.
type Environment struct {
	Cwd     string
	IsGit   bool
	ModelID string
}

// WithEnvironment registers the environment section. Callers should refresh
// it via sys.Refresh("environment") when cwd changes mid-session.
func WithEnvironment(env Environment) Option {
	return func(sys core.System, _ core.Scope) {
		sys.Use(core.Section{
			Slot: core.SlotEnvironment, Name: "environment", Source: core.Dynamic,
			Render: func() string { return renderEnvironment(env) },
		}, "system:init")
	}
}

func renderEnvironment(env Environment) string {
	git := "no"
	if env.IsGit {
		git = "yes"
	}
	body := fmt.Sprintf(
		"date: %s\ncwd: %s\ngit: %s\nplatform: %s/%s\nmodel: %s",
		time.Now().Format("2006-01-02"),
		env.Cwd, git, runtime.GOOS, runtime.GOARCH, env.ModelID,
	)
	return wrap("environment", nil, body)
}
