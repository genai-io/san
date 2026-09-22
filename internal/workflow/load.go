package workflow

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Saved is a workflow definition found on disk. Only the frontmatter is read
// while listing; Parse runs when one is actually launched, so a broken
// definition is reported to whoever tried to run it rather than at startup.
type Saved struct {
	Name        string
	Description string
	Path        string
}

// Load lists the `*.md` definitions in dirs, earliest directory winning when
// a name appears twice, sorted by name. Callers pass the directories in
// priority order; a missing directory is not an error.
func Load(dirs ...string) []Saved {
	var out []Saved
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			s := Saved{Name: strings.TrimSuffix(e.Name(), ".md"), Path: path}
			// Only the head is read and only the header parsed: listing a
			// directory should cost neither the whole of a file nobody is
			// running nor the graph inside it. A definition whose header does
			// not parse still lists, under its filename.
			var header Workflow
			if _, err := header.parseFrontmatter(head(path)); err == nil {
				if header.Name != "" {
					s.Name = header.Name
				}
				s.Description = header.Description
			}
			if seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b Saved) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Find returns the definition named name, parsed and validated.
func Find(name string, dirs ...string) (*Workflow, error) {
	for _, s := range Load(dirs...) {
		if s.Name != name {
			continue
		}
		b, err := os.ReadFile(s.Path)
		if err != nil {
			return nil, err
		}
		w, err := Parse(string(b))
		if err != nil {
			return nil, err
		}
		if w.Name == "" {
			w.Name = name
		}
		return w, nil
	}
	return nil, fmt.Errorf("no workflow named %s: %w", name, ErrNotFound)
}

// ErrNotFound reports a name that matched no definition, so a caller can
// tell it from a definition that was found but would not parse.
var ErrNotFound = errors.New("no such workflow")

// head returns the first headBytes of a file, enough for any frontmatter.
func head(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, headBytes))
	if err != nil {
		return ""
	}
	return string(b)
}

const headBytes = 4 << 10
