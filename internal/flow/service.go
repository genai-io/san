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
	mainExecutor MainExecutor
	executor     tool.AgentExecutor
	tasks        *task.Manager
	dirs         []string
	mu           sync.Mutex
	store        *Store
	live         map[string]*task.AgentTask
}

func NewService(tasks *task.Manager) *Service { return &Service{tasks: tasks} }
func (s *Service) SetExecutor(executor tool.AgentExecutor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executor = executor
}

// SetSearchPaths sets the definition directories in priority order. Run
// records live beside the first one; the store is replaced only when that
// location changes, so live runs and new lookups share one store.
func (s *Service) SetSearchPaths(dirs []string) {
	s.dirs = append([]string(nil), dirs...)
	dir := ""
	if len(dirs) > 0 {
		dir = RunDir(dirs[0])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil || len(dirs) > 0 && s.store.dir != dir {
		s.store = NewStore(dir)
	}
}

func (s *Service) SavedDefinitions() []SavedDefinition { return ListDefinitions(s.dirs...) }

func (s *Service) SavedInputNames(name string) ([]string, error) {
	b, err := FindDefinition(name, s.dirs...)
	if err != nil {
		return nil, err
	}
	return b.InputNames(), nil
}

func (s *Service) Definition(name string) (string, error) { return ReadDefinition(name, s.dirs...) }

// SaveDefinition preserves source-conflict checking for reviewed project copies.
func (s *Service) SaveDefinition(name, definition, baseline string) error {
	return SaveDefinition(name, definition, baseline, s.dirs...)
}

func (s *Service) preflight(b *Blueprint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executor == nil {
		for _, n := range b.Nodes {
			if n.Executor != "main" {
				return errors.New("agent executor not configured")
			}
		}
	}
	if s.tasks == nil {
		return errors.New("task manager not configured")
	}
	for _, n := range b.Nodes {
		if n.Executor == "main" {
			continue
		}
		agent := n.Config["agent"]
		info, _, ok := s.executor.ResolveAgentSelection(agent)
		if !ok {
			return fmt.Errorf("node %s: agent %q is disabled", n.ID, agent)
		}
		if agent != "" && info.Source == "" {
			return fmt.Errorf("node %s: no agent named %q — drop the line for the default agent, or set mode: explore|edit for a read-only or editing node", n.ID, agent)
		}
	}
	return nil
}

func (s *Service) named(name string) (*Blueprint, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("flow name is required")
	}
	b, err := FindDefinition(name, s.dirs...)
	if errors.Is(err, ErrNotFound) {
		var saved strings.Builder
		for _, d := range s.SavedDefinitions() {
			saved.WriteString("\n- " + d.Name)
		}
		if saved.Len() == 0 {
			saved.WriteString(" (none saved)")
		}
		return nil, fmt.Errorf("%w. Saved flows:%s", err, saved.String())
	}
	if err != nil {
		return nil, fmt.Errorf("flow %s is invalid:\n%w", name, err)
	}
	if err := s.preflight(b); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Service) Preview(name string, width int) (string, error) {
	b, err := s.named(name)
	if err != nil {
		return "", err
	}
	width = fitWidth(width)
	turns, fanOut := b.Bounds()
	preview := fmt.Sprintf("Preview only · up to %d turns · max %d parallel\n\n%s", turns, b.MaxParallel, b.CompactProgressView(nil, width))
	if b.Description != "" {
		preview += "\nAbout: " + ansi.Truncate(b.Description, max(8, width-7), "…")
	}
	for _, line := range fanOut {
		preview += "\n" + line
	}
	if steps := b.PreviewSteps(width); steps != "" {
		preview += "\nSteps:\n" + steps
	}
	inputs := b.InputNames()
	if len(inputs) > 0 {
		preview += "\nInputs: " + strings.Join(inputs, ", ")
	}
	preview += "\nRun: /flow run " + command.QuoteArgument(name)
	for _, key := range inputs {
		preview += " " + key + "=<value>"
	}
	return preview, nil
}

// Launch validates and schedules a named, reviewed definition and returns the
// scheduler task ID.
func (s *Service) Launch(name string, inputs map[string]string, width int) (string, error) {
	b, err := s.named(name)
	if err != nil {
		return "", err
	}
	if err := validateInputs(b, inputs); err != nil {
		return "", err
	}
	return s.start(b, inputs, "/flow run "+command.QuoteArgument(b.Name), width)
}

func validateInputs(b *Blueprint, inputs map[string]string) error {
	var missing []string
	for _, key := range b.InputNames() {
		if _, ok := inputs[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("flow %s needs input %s; use /flow show %s to see the run command", b.Name, strings.Join(missing, ", "), command.QuoteArgument(b.Name))
	}
	return nil
}
