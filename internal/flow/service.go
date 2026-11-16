package flow

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
)

// Service schedules reviewed blueprints through the native subagent executor.
type Service struct {
	executor tool.AgentExecutor
	tasks    *task.Manager
	dirs     []string
	mu       sync.Mutex
	store    *Store
	live     map[string]*task.AgentTask
}

func NewService(tasks *task.Manager) *Service              { return &Service{tasks: tasks} }
func (t *Service) SetExecutor(executor tool.AgentExecutor) { t.executor = executor }
func (t *Service) SetSearchPaths(dirs []string) {
	t.dirs = append([]string(nil), dirs...)
	dir := ""
	if len(dirs) > 0 {
		dir = runDir(dirs[0])
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.store == nil || len(dirs) > 0 {
		t.store = NewStore(dir)
	}
}
func (t *Service) Saved() string {
	var b strings.Builder
	for _, s := range t.SavedDefinitions() {
		b.WriteString("\n- " + s.Name)
		if s.Description != "" {
			b.WriteString(" — " + s.Description)
		}
	}
	return b.String()
}
func (t *Service) SavedDefinitions() []Saved { return Load(t.dirs...) }
func (t *Service) SavedInputNames(name string) ([]string, error) {
	w, err := Find(name, t.dirs...)
	if err != nil {
		return nil, err
	}
	return w.InputNames(), nil
}
func (t *Service) preflight(w *Blueprint) error {
	if t.executor == nil {
		return errors.New("agent executor not configured")
	}
	if t.tasks == nil {
		return errors.New("task manager not configured")
	}
	for _, n := range w.Nodes {
		agent := n.Config["agent"]
		info, _, ok := t.executor.ResolveAgentSelection(agent)
		if !ok {
			return fmt.Errorf("node %s: agent %q is disabled", n.ID, agent)
		}
		if agent != "" && info.Source == "" {
			return fmt.Errorf("node %s: no agent named %q — drop the line for the default agent, or set mode: explore|edit for a read-only or editing node", n.ID, agent)
		}
	}
	return nil
}
func (t *Service) named(name string) (*Blueprint, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("flow name is required")
	}
	w, err := Find(name, t.dirs...)
	if errors.Is(err, ErrNotFound) {
		saved := t.Saved()
		if saved == "" {
			saved = " (none saved)"
		}
		return nil, fmt.Errorf("%w. Saved flows:%s", err, saved)
	}
	if err != nil {
		return nil, fmt.Errorf("flow %s is invalid:\n%w", name, err)
	}
	if err := t.preflight(w); err != nil {
		return nil, err
	}
	return w, nil
}
func describe(w *Blueprint, description string) string {
	name := w.Name
	if name == "" {
		name = "(unnamed)"
	}
	if description == "" {
		description = w.Description
	}
	turns, fanOut := w.Bounds()
	s := fmt.Sprintf("Run flow %s: %d nodes, up to %d subagent turns, max %d parallel", name, len(w.Nodes), turns, w.MaxParallel)
	if description != "" {
		s += " — " + description
	}
	for _, line := range fanOut {
		s += "\n" + line
	}
	return s
}
func (t *Service) Preview(name string, width int) (string, error) {
	w, err := t.named(name)
	if err != nil {
		return "", err
	}
	turns, fanOut := w.Bounds()
	preview := fmt.Sprintf("Preview only · up to %d turns · max %d parallel\n\n%s", turns, w.MaxParallel, w.CompactProgressView(nil, flowDisplayWidth(width)))
	if w.Description != "" {
		preview += "\nAbout: " + ansi.Truncate(w.Description, max(8, flowDisplayWidth(width)-7), "…")
	}
	for _, line := range fanOut {
		preview += "\n" + line
	}
	if steps := w.PreviewSteps(flowDisplayWidth(width)); steps != "" {
		preview += "\nSteps:\n" + steps
	}
	inputs := w.InputNames()
	if len(inputs) > 0 {
		preview += "\nInputs: " + strings.Join(inputs, ", ")
	}
	preview += "\nRun: /flow run " + command.QuoteArgument(name)
	for _, key := range inputs {
		preview += " " + key + "=<value>"
	}
	return preview, nil
}

// startInline validates and schedules an unsaved blueprint. Only tests use it;
// users review and save definitions, then Launch them by name.
func (t *Service) startInline(definition string, inputs map[string]string, width int) (bounds, taskID string, err error) {
	w, err := Parse(definition)
	if err != nil {
		return "", "", fmt.Errorf("invalid flow:\n%w", err)
	}
	if err := t.preflight(w); err != nil {
		return "", "", err
	}
	return t.launch(w, inputs, width)
}

// Launch validates and schedules a named, reviewed definition.
func (t *Service) Launch(name string, inputs map[string]string, width int) (bounds, taskID string, err error) {
	w, err := t.named(name)
	if err != nil {
		return "", "", err
	}
	return t.launch(w, inputs, width)
}
func validateInputs(w *Blueprint, inputs map[string]string) error {
	var missing []string
	for _, key := range w.InputNames() {
		if _, ok := inputs[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("flow %s needs input %s; use /flow show %s to see the run command", w.Name, strings.Join(missing, ", "), command.QuoteArgument(w.Name))
	}
	return nil
}
func (t *Service) launch(w *Blueprint, inputs map[string]string, width int) (string, string, error) {
	if err := validateInputs(w, inputs); err != nil {
		return "", "", err
	}
	turns, _ := w.Bounds()
	runID, taskID, err := t.start(w, inputs, "/flow run "+command.QuoteArgument(w.Name), width)
	summary := fmt.Sprintf("Started flow %s · up to %d turns · max %d parallel\nRun ID: %s", w.Name, turns, w.MaxParallel, runID)
	return summary, taskID, err
}
func (t *Service) Definition(name string) (string, error) { return ReadDefinition(name, t.dirs...) }

// SaveDefinition preserves source-conflict checking for reviewed project copies.
func (t *Service) SaveDefinition(name, definition, baseline string) error {
	return SaveDefinition(name, definition, baseline, t.dirs...)
}
