package subagent

import (
	"github.com/genai-io/san/internal/setting"
)

// Matches reports whether any rule in the list matches the call. Bash
// compound commands match if any subcommand matches. Use for deny / ask
// semantics where any-subcommand match should fire.
func (t ToolList) Matches(name string, input map[string]any) bool {
	if t == nil {
		return false
	}
	// Fast path: no patterns means a name walk suffices and we can skip
	// BuildRule, which parses the bash AST for Bash calls.
	if !t.HasPattern(name) {
		return t.HasName(name)
	}
	rule := setting.BuildRule(name, input)
	for _, r := range t {
		if r.Name != name {
			continue
		}
		if r.Pattern == "" {
			return true
		}
		if setting.MatchesToolPattern(name, input, rule, r.Rule()) {
			return true
		}
	}
	return false
}

// Allows reports whether the list permits the call as an allow rule. Bash
// compound commands require every subcommand to be covered. Use for allow
// semantics; pair with Matches for deny / ask.
func (t ToolList) Allows(name string, input map[string]any) bool {
	if t == nil {
		return false
	}
	// A bare entry for the tool is unconditional permission and short-circuits
	// the per-subcommand check below.
	if !t.HasPattern(name) {
		return t.HasName(name)
	}
	patterns := make([]string, 0, len(t))
	hasBare := false
	for _, r := range t {
		if r.Name != name {
			continue
		}
		if r.Pattern == "" {
			hasBare = true
			break
		}
		patterns = append(patterns, r.Rule())
	}
	if hasBare {
		return true
	}
	_, ok := setting.MatchAllowList(name, input, patterns)
	return ok
}
