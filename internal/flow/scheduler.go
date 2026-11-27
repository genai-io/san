package flow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
)

// Scheduler schedules reviewed blueprints through the native subagent executor.
type Scheduler struct {
	mainExecutor MainExecutor
	executor     worker
	tasks        *task.RunSet
	dirs         []string
	mu           sync.Mutex
	store        *runStore
	live         map[string]*task.AgentTask
}

func NewScheduler(tasks *task.RunSet) *Scheduler { return &Scheduler{tasks: tasks} }
func (s *Scheduler) SetExecutor(executor worker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executor = executor
}

// SetSearchPaths sets the definition directories in priority order. Run
// records live beside the first one; the store is replaced only when that
// location changes, so live runs and new lookups share one store.
func (s *Scheduler) SetSearchPaths(dirs []string) {
	s.dirs = append([]string(nil), dirs...)
	dir := ""
	if len(dirs) > 0 {
		dir = RunDir(dirs[0])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil || len(dirs) > 0 && s.store.dir != dir {
		s.store = newRunStore(dir)
	}
}

func (s *Scheduler) SavedDefinitions() []SavedDefinition { return ListDefinitions(s.dirs...) }

func (s *Scheduler) SavedInputNames(name string) ([]string, error) {
	b, err := FindDefinition(name, s.dirs...)
	if err != nil {
		return nil, err
	}
	return b.InputNames(), nil
}

func (s *Scheduler) Definition(name string) (string, error) { return ReadDefinition(name, s.dirs...) }

// SaveDefinition preserves source-conflict checking for reviewed project copies.
func (s *Scheduler) SaveDefinition(name, definition, baseline string) error {
	return SaveDefinition(name, definition, baseline, s.dirs...)
}

type workerBinding struct {
	configs  map[string]any
	policies map[string]string
}

// PrepareFlowWorker binds the effective host policy and identifies it for recovery
// without persisting private agent configuration in the run record.
type worker interface {
	Run(context.Context, tool.AgentExecRequest) (*tool.AgentExecResult, error)
	ResolveAgentSelection(string) (tool.AgentConfigInfo, any, bool)
	PrepareFlowWorker(tool.AgentExecRequest) (resolved any, fingerprint string, err error)
}

func (s *Scheduler) bindWorkers(b *Blueprint, executor worker) (*workerBinding, error) {
	if s.tasks == nil {
		return nil, errors.New("task manager not configured")
	}
	workers := &workerBinding{configs: make(map[string]any), policies: make(map[string]string)}
	for _, n := range b.Nodes {
		if n.Executor == "main" {
			continue
		}
		if executor == nil {
			return nil, errors.New("agent executor not configured")
		}
		agent := n.Config["agent"]
		info, config, ok := executor.ResolveAgentSelection(agent)
		if !ok {
			return nil, fmt.Errorf("node %s: agent %q is disabled", n.ID, agent)
		}
		if agent != "" && info.Source == "" {
			return nil, fmt.Errorf("node %s: no agent named %q — drop the line for the default agent, or set mode: explore|edit for a read-only or editing node", n.ID, agent)
		}
		if config == nil {
			return nil, fmt.Errorf("node %s: executor cannot bind resolved worker policy", n.ID)
		}
		config, fingerprint, err := executor.PrepareFlowWorker(tool.AgentExecRequest{Agent: agent, Mode: n.Config["mode"], Model: n.Config["model"], AllowTools: slices.Clone(n.AllowTools), DenyTools: slices.Clone(n.DenyTools), ResolvedAgentConfig: config})
		if err != nil {
			return nil, fmt.Errorf("node %s: fingerprint worker policy: %w", n.ID, err)
		}
		if config == nil || fingerprint == "" {
			return nil, fmt.Errorf("node %s: missing worker policy fingerprint", n.ID)
		}
		workers.configs[n.ID] = config
		workers.policies[n.ID] = fingerprint
	}
	return workers, nil
}

func (s *Scheduler) named(name string) (*Blueprint, error) {
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
	return b, nil
}

func (s *Scheduler) Preview(name string, width int) (string, error) {
	b, err := s.named(name)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	executor := s.executor
	s.mu.Unlock()
	if _, err := s.bindWorkers(b, executor); err != nil {
		return "", err
	}
	width = fitWidth(width)
	turns, fanOut := b.Bounds()
	unit := "turns"
	if turns == 1 {
		unit = "turn"
	}
	preview := fmt.Sprintf("Preview only · up to %d %s · max %d parallel\n\n%s", turns, unit, b.MaxParallel, b.CompactProgressView(nil, width))
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
func (s *Scheduler) Launch(name string, inputs map[string]string, width int) (string, error) {
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
		if strings.TrimSpace(inputs[key]) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("flow %s needs input %s; use /flow show %s to see the run command", b.Name, strings.Join(missing, ", "), command.QuoteArgument(b.Name))
	}
	return nil
}
