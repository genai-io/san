package persona

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/markdown"
	"gopkg.in/yaml.v3"
)

// loadIdentities scans a legacy identities/ directory, returning each *.md file
// as a skill-less persona whose Identity part is the file body. This is the
// compatibility path by which personas absorb the older single-file identities
// under ~/.san/identities/ and <project>/.san/identities/.
func loadIdentities(dir string, scope Scope) []*Persona {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*Persona
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.EqualFold(e.Name(), "README.md") {
			continue
		}
		if p, ok := parseIdentityFile(filepath.Join(dir, e.Name())); ok {
			p.Scope = scope
			out = append(out, p)
		}
	}
	return out
}

// parseIdentityFile turns a legacy identity .md (YAML frontmatter with name /
// description, then a markdown body) into a skill-less persona: the body
// becomes the identity part, with no skills and no settings overlay.
func parseIdentityFile(path string) (*Persona, bool) {
	fm, body, err := markdown.ParseFrontmatterFile(path)
	if err != nil {
		return nil, false
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if fm != "" {
		_ = yaml.Unmarshal([]byte(fm), &meta)
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = filenameStem(path)
	}
	if strings.EqualFold(name, DefaultName) {
		return nil, false // "default" is reserved/virtual
	}
	return &Persona{
		Name:        name,
		Description: strings.TrimSpace(meta.Description),
		Dir:         path,
		Identity:    strings.TrimSpace(body),
	}, true
}

func filenameStem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
