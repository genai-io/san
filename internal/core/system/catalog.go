package system

import (
	"embed"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/genai-io/san/internal/core"
)

// Embedded prompt templates. Layout:
//
//	prompts/identity.txt              — one-line persona preamble (the "identity" part)
//	prompts/output.txt                — communication style (Tone / Updates / Behavior)
//	prompts/engineering.txt           — engineering defaults (Restraint / Conventions / Errors)
//	prompts/policy.txt                — safety contract
//	prompts/compact.txt               — conversation compactor prompt
//	prompts/guidelines/{tools,system-reminders,git,questions,tasks}.txt
//	prompts/providers/<name>.txt      — provider-specific quirks (optional)
//
// These compose into four parts, top to bottom:
//
//	You are San, …                    (identity, raw preamble)
//	<behavior> … </behavior>          (output + engineering — main agent only)
//	<rules> … </rules>                (policy + guidelines + provider, scope-aware)
//	<environment> … </environment>    (volatile footer)
//
// Identity is bare because Anthropic's standard preamble shape starts with
// "You are X". The other parts live in a named XML envelope so the model can
// address each as a structured unit, and so a persona can replace a whole
// part by dropping in one file (system/<part>.md).
//
//go:embed prompts/*.txt prompts/guidelines/*.txt
var promptFS embed.FS

// init-time read of every static template. Keeps Build() allocation-light.
var (
	cachedIdentity    = loadEmbed("prompts/identity.txt")
	cachedOutput      = loadEmbed("prompts/output.txt")
	cachedEngineering = loadEmbed("prompts/engineering.txt")
	cachedPolicy      = loadEmbed("prompts/policy.txt")
	cachedCompact     = loadEmbed("prompts/compact.txt")
	cachedTools       = loadEmbed("prompts/guidelines/tools.txt")
	cachedGit         = loadEmbed("prompts/guidelines/git.txt")
	cachedQuestions   = loadEmbed("prompts/guidelines/questions.txt")
	cachedTasks       = loadEmbed("prompts/guidelines/tasks.txt")
	cachedReminders   = loadEmbed("prompts/guidelines/system-reminders.txt")
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
// (a persona or user-defined identity) replaces the built-in default. Rendered
// raw (no XML envelope) to match Anthropic's standard "You are X" preamble.
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
func behaviorSection() core.Section {
	return core.Section{
		Slot: core.SlotBehavior, Name: "behavior", Source: core.Predefined,
		Render: func() string {
			return wrap("behavior", nil, cachedOutput+"\n\n"+cachedEngineering)
		},
	}
}

// Part: rules (slot 2)

// rulesSection renders the safety contract plus the operational protocols
// (tools and system-reminders always; task tracking and interactive questions
// for the main agent), with git safety folded in when isGit and any provider
// quirks appended last. Subagents get the safety + tool subset.
func rulesSection(scope core.Scope, isGit bool, provider string) core.Section {
	return core.Section{
		Slot: core.SlotRules, Name: "rules", Source: core.Predefined,
		Render: func() string {
			return wrap("rules", nil, assembleRules(scope, isGit, provider))
		},
	}
}

func assembleRules(scope core.Scope, isGit bool, provider string) string {
	blocks := []string{
		headed("Safety", cachedPolicy),
		headed("Tools", cachedTools),
		headed("System reminders", cachedReminders),
	}
	if scope == core.ScopeMain {
		// Task tracking + interactive questions are main-agent behaviors.
		blocks = append(blocks,
			headed("Task tracking", cachedTasks),
			headed("Asking the user", cachedQuestions),
		)
	}
	if isGit {
		blocks = append(blocks, headed("Git safety", cachedGit))
	}
	if provider != "" {
		if quirks := loadEmbedOptional("prompts/providers/" + provider + ".txt"); quirks != "" {
			blocks = append(blocks, headed("Provider notes", quirks))
		}
	}
	return strings.Join(blocks, "\n\n")
}

// headed prefixes a rules sub-block with a markdown heading so the merged
// <rules> envelope stays legible once several blocks are concatenated.
func headed(title, body string) string {
	return "## " + title + "\n\n" + strings.TrimSpace(body)
}

// Options

// WithIdentity replaces the default identity with a persona/user-defined one,
// e.g. an "ML engineer" charter. An empty string keeps the default.
func WithIdentity(text string) Option {
	return func(cfg *buildConfig) { cfg.identity = strings.TrimSpace(text) }
}

// SwapIdentity replaces the identity part on an already-built system. Empty
// text reverts to the built-in default. Visible on the next sys.Prompt().
func SwapIdentity(sys core.System, text string) {
	sys.Use(identitySection(text), "command:identity")
}

// WithProvider folds provider-specific quirks (prompts/providers/<name>.txt,
// optional) into the rules part. An empty or unmatched name is a no-op.
func WithProvider(name string) Option {
	return func(cfg *buildConfig) { cfg.provider = name }
}

// WithGitGuidelines includes the git-safety rules. Off by default.
func WithGitGuidelines(isGit bool) Option {
	return func(cfg *buildConfig) { cfg.isGit = isGit }
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
	fmt.Fprintf(&sb, "You are a %s subagent operating inside San.\n", b.AgentName)
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

// Part: environment (slot 3, volatile)

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
