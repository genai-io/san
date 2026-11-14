package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/workflow"
)

// Serialize definition saves without coupling them to running executions.
var definitionWrites sync.Mutex

var definitionName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func (t *WorkflowTool) Definitions() []workflow.Saved { return workflow.Load(t.dirs...) }

func (t *WorkflowTool) Definition(name string) (string, error) {
	for _, entry := range t.Definitions() {
		if entry.Name == name {
			data, err := os.ReadFile(entry.Path)
			return string(data), err
		}
	}
	return "", fmt.Errorf("no workflow named %s: %w", name, workflow.ErrNotFound)
}

// SaveDefinition saves a reviewed project copy. baseline is the source the
// editor opened; a changed source must be reviewed again before replacement.
func (t *WorkflowTool) SaveDefinition(name, definition, baseline string) error {
	if len(t.dirs) == 0 {
		return fmt.Errorf("workflow directory is not configured")
	}
	if !definitionName.MatchString(name) {
		return fmt.Errorf("workflow name must start with a letter and contain only letters, numbers, - or _")
	}
	w, err := workflow.Parse(definition)
	if err != nil {
		return err
	}
	if w.Name != "" && w.Name != name {
		return fmt.Errorf("frontmatter name %q must match %q", w.Name, name)
	}

	definitionWrites.Lock()
	defer definitionWrites.Unlock()

	target := filepath.Join(t.dirs[0], name+".md")
	found := false
	for _, entry := range t.Definitions() {
		if entry.Name != name {
			continue
		}
		found = true
		current, err := os.ReadFile(entry.Path)
		if err != nil {
			return err
		}
		if baseline == "" {
			return fmt.Errorf("workflow %s already exists; open it for review before editing", name)
		}
		if string(current) != baseline {
			return fmt.Errorf("workflow %s changed on disk; reopen it and review the changes", name)
		}
		// An existing project definition may have a filename different from
		// its frontmatter name. Replace that file, not a duplicate definition.
		if filepath.Clean(filepath.Dir(entry.Path)) == filepath.Clean(t.dirs[0]) {
			target = entry.Path
		}
		break
	}
	if !found && baseline != "" {
		return fmt.Errorf("workflow %s was removed; reopen it before saving", name)
	}
	if data, err := os.ReadFile(target); err == nil && (!found || string(data) != baseline) {
		return fmt.Errorf("%s already contains another definition; choose a different name", target)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return atomicfile.Write(target, []byte(definition), 0o600)
}
