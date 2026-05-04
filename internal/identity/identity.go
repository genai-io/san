// Package identity manages user-defined personas that override Gen Code's
// default system-prompt identity.
//
// An identity is a markdown file with frontmatter:
//
//	---
//	name: ml-engineer
//	description: ML engineering specialist (PyTorch, JAX)
//	---
//
//	You are an ML engineer ...
//
// Identities live under:
//   - ~/.gen/identities/<name>.md  (user level)
//   - .gen/identities/<name>.md    (project level — overrides user)
//
// The "default" identity is virtual: it represents Gen Code's built-in
// prompts/identity.txt and has no file.
package identity

import (
	"sort"
	"strings"

	"github.com/genai-io/gen-code/internal/markdown"
	"gopkg.in/yaml.v3"
)

// DefaultName is the reserved name of the virtual built-in identity.
// An empty settings.identity and "default" are equivalent — both mean
// "use the embedded prompts/identity.txt".
const DefaultName = "default"

// Scope tells where an identity was loaded from.
type Scope int

const (
	ScopeBuiltin Scope = iota // virtual default identity
	ScopeUser                 // ~/.gen/identities/
	ScopeProject              // .gen/identities/  (overrides user)
)

// Identity is one persona definition.
type Identity struct {
	Name        string // filename without .md, kebab-case
	Description string // one-liner from frontmatter
	Body        string // markdown body, used as system-prompt identity content
	Path        string // absolute path to the .md file ("" for default)
	Scope       Scope
}

// IsBuiltin reports whether this identity is the virtual default.
func (i Identity) IsBuiltin() bool { return i.Scope == ScopeBuiltin }

// DefaultIdentity returns the virtual built-in identity entry shown in the
// selector. Its Body is empty — the actual default content is rendered by
// system.Build's stock identity section, not from this struct.
func DefaultIdentity() *Identity {
	return &Identity{
		Name:        DefaultName,
		Description: "Built-in Gen Code persona — software engineering generalist",
		Scope:       ScopeBuiltin,
	}
}

// frontmatter is the parsed yaml header.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// parseFile reads an identity .md file and returns its parsed Identity.
// The frontmatter is best-effort; missing fields fall back to filename / "".
func parseFile(path string) (*Identity, error) {
	fm, body, err := markdown.ParseFrontmatterFile(path)
	if err != nil {
		return nil, err
	}
	var meta frontmatter
	if fm != "" {
		_ = yaml.Unmarshal([]byte(fm), &meta)
	}
	id := &Identity{
		Name:        strings.TrimSpace(meta.Name),
		Description: strings.TrimSpace(meta.Description),
		Body:        strings.TrimSpace(body),
		Path:        path,
	}
	if id.Name == "" {
		id.Name = filenameStem(path)
	}
	return id, nil
}

// sortIdentities orders identities for stable display: default first, then
// project-level, then user-level, alphabetical within each group.
//
// Note: this differs from raw Scope ordering. Scope values rank precedence
// when resolving collisions (project > user, see reload), but for display
// project-level entries should appear above user-level ones because they
// are more specific to the current context.
func sortIdentities(items []*Identity) {
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
