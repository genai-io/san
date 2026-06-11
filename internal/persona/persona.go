// Package persona manages user-defined personas: switchable bundles of a
// system prompt, a skill set, and a config overlay.
//
// A persona is a directory:
//
//	<name>/
//	  system/
//	    identity.md   (optional) overrides the identity part
//	    behavior.md   (optional) overrides the behavior part
//	    rules.md      (optional) overrides the rules part
//	  skills/
//	    <skill>/SKILL.md   persona-scoped skills
//	  settings.json   (optional) description + skill states + config overlay
//
// Personas live under:
//   - ~/.san/personas/<name>/   (user level)
//   - .san/personas/<name>/     (project level — overrides user)
//
// The "default" persona is virtual: it represents San's built-in prompt with
// no skills and no overlay, and has no directory.
//
// This package only discovers, parses, and serves personas. Applying one
// (system-prompt override, skill activation, settings overlay) belongs to the
// system, skill, and app layers.
package persona

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultName is the reserved name of the virtual built-in persona. An empty
// selection and "default" are equivalent — both mean "use San's built-ins".
const DefaultName = "default"

// Scope tells where a persona was loaded from.
type Scope int

const (
	ScopeBuiltin Scope = iota // virtual default persona
	ScopeUser                 // ~/.san/personas/
	ScopeProject              // .san/personas/  (overrides user)
)

// Persona is one persona bundle parsed from disk.
type Persona struct {
	Name        string // directory name, kebab-case
	Description string // one-liner from settings.json
	Dir         string // absolute path to the persona directory ("" for default)
	Scope       Scope

	// System-prompt part overrides — raw markdown bodies. An empty string
	// means "use San's built-in default for that part".
	Identity string
	Behavior string
	Rules    string

	// SkillDirs are absolute paths to skill directories (each holding a
	// SKILL.md) bundled with this persona.
	SkillDirs []string

	// Settings is the parsed settings.json (description, skill states, config
	// overlay), or nil when the persona has no settings.json.
	Settings *Settings
}

// IsBuiltin reports whether this persona is the virtual default.
func (p Persona) IsBuiltin() bool { return p.Scope == ScopeBuiltin }

// DefaultPersona returns the virtual built-in persona shown in the selector.
// Its part overrides are empty — the built-in prompt is rendered by the system
// builder, not from this struct.
func DefaultPersona() *Persona {
	return &Persona{
		Name:        DefaultName,
		Description: "Built-in San persona — software engineering generalist",
		Scope:       ScopeBuiltin,
	}
}

// parseDir loads a persona from a directory. It returns (nil, false) when the
// directory is not a persona — no system/ part files, no skills, and no
// settings.json.
func parseDir(dir string) (*Persona, bool) {
	p := &Persona{Name: filepath.Base(dir), Dir: dir}
	found := false

	// system/<part>.md overrides — pure markdown bodies (no frontmatter).
	sysDir := filepath.Join(dir, "system")
	p.Identity = readPart(sysDir, "identity.md")
	p.Behavior = readPart(sysDir, "behavior.md")
	p.Rules = readPart(sysDir, "rules.md")
	if p.Identity != "" || p.Behavior != "" || p.Rules != "" {
		found = true
	}

	// skills/<name>/SKILL.md bundle.
	p.SkillDirs = skillDirs(filepath.Join(dir, "skills"))
	if len(p.SkillDirs) > 0 {
		found = true
	}

	// settings.json — description + skill states + config overlay.
	if s, ok := parseSettings(filepath.Join(dir, "settings.json")); ok {
		p.Settings = s
		p.Description = s.Description
		found = true
	}

	if !found {
		return nil, false
	}
	return p, true
}

// readPart reads system/<file>, trimmed. A missing file yields "".
func readPart(sysDir, file string) string {
	data, err := os.ReadFile(filepath.Join(sysDir, file))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// skillDirs returns the immediate subdirectories of root that contain a
// SKILL.md (the persona's bundled skills).
func skillDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(root, e.Name())
		if hasSkillFile(sub) {
			out = append(out, sub)
		}
	}
	return out
}

func hasSkillFile(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(e.Name(), "SKILL.md") {
			return true
		}
	}
	return false
}

// sortPersonas orders personas for stable display: default first, then
// project-level, then user-level, alphabetical within each group. Display
// order differs from Scope precedence (which ranks project > user when
// resolving name collisions) — project entries are shown above user ones
// because they are more specific to the current context.
func sortPersonas(items []*Persona) {
	sort.Slice(items, func(i, j int) bool {
		wi, wj := displayWeight(items[i].Scope), displayWeight(items[j].Scope)
		if wi != wj {
			return wi < wj
		}
		return items[i].Name < items[j].Name
	})
}

func displayWeight(s Scope) int {
	switch s {
	case ScopeBuiltin:
		return 0
	case ScopeProject:
		return 1
	case ScopeUser:
		return 2
	}
	return 3
}
