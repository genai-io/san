package input

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
)

type agentRegistryStub struct {
	configs  []tool.AgentConfigInfo
	disabled map[bool]map[string]bool
	setName  string
	setState bool
	setUser  bool
	setErr   error
}

func (s *agentRegistryStub) ListConfigs() []tool.AgentConfigInfo { return s.configs }
func (s *agentRegistryStub) GetDisabledAt(user bool) map[string]bool {
	return s.disabled[user]
}
func (s *agentRegistryStub) SetEnabled(name string, enabled, user bool) error {
	s.setName, s.setState, s.setUser = name, enabled, user
	return s.setErr
}

func TestAgentSelectorListsCustomAgentsAndTogglesTheirScope(t *testing.T) {
	reg := &agentRegistryStub{
		configs: []tool.AgentConfigInfo{
			{Name: "project-reviewer", Source: "project"},
			{Name: "user-researcher", Source: "user"},
		},
		disabled: map[bool]map[string]bool{false: {}, true: {}},
	}
	selector := NewAgentSelector(func() AgentRegistry { return reg })
	if err := selector.EnterSelect(100, 40); err != nil {
		t.Fatal(err)
	}
	if len(selector.list.items) != 2 {
		t.Fatalf("listed agents = %d, want 2", len(selector.list.items))
	}
	if selector.list.activeTab != int(agentTabProject) {
		t.Fatalf("active tab = %d, want project", selector.list.activeTab)
	}

	cmd := selector.Toggle()
	if cmd == nil {
		t.Fatal("toggle returned no command")
	}
	msg, ok := cmd().(AgentToggleMsg)
	if !ok || msg.AgentName != "project-reviewer" || msg.Enabled {
		t.Fatalf("toggle message = %#v, want disabled project-reviewer", msg)
	}
	if reg.setName != "project-reviewer" || reg.setState || reg.setUser {
		t.Fatalf("SetEnabled args = %q, %v, %v", reg.setName, reg.setState, reg.setUser)
	}

	selector.list.activeTab = int(agentTabUser)
	selector.list.filtered = []agentItem{{Name: "user-researcher", Source: "user", Enabled: true}}
	selector.list.nav.Selected = 0
	selector.Toggle()
	if reg.setName != "user-researcher" || !reg.setUser {
		t.Fatalf("user SetEnabled args = %q, user=%v", reg.setName, reg.setUser)
	}
}

func TestAgentSelectorToggleFailureLeavesStateUnchanged(t *testing.T) {
	reg := &agentRegistryStub{
		configs:  []tool.AgentConfigInfo{{Name: "reviewer", Source: "project"}},
		disabled: map[bool]map[string]bool{false: {}, true: {}},
		setErr:   errors.New("read-only filesystem"),
	}
	selector := NewAgentSelector(func() AgentRegistry { return reg })
	if err := selector.EnterSelect(100, 40); err != nil {
		t.Fatal(err)
	}

	msg, ok := selector.Toggle()().(AgentToggleMsg)
	if !ok || msg.Err == nil {
		t.Fatalf("toggle message = %#v, want persistence error", msg)
	}
	if !selector.list.filtered[0].Enabled || !selector.list.items[0].Enabled {
		t.Fatal("failed toggle changed the displayed state")
	}
}

func TestAgentSelectorTogglePersistsThroughRegistryStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	reg := subagent.NewRegistry()
	reg.Register(&subagent.AgentConfig{Name: "project-reviewer", Source: "project"})
	if err := reg.InitStores(cwd); err != nil {
		t.Fatal(err)
	}
	adapter := &agentRegistryAdapterForTest{reg: reg}
	selector := NewAgentSelector(func() AgentRegistry { return adapter })
	if err := selector.EnterSelect(100, 40); err != nil {
		t.Fatal(err)
	}
	selector.Toggle()

	reloaded := subagent.NewAgentStore(filepath.Join(cwd, ".san", "agents.json"))
	if !reloaded.IsDisabled("project-reviewer") {
		t.Fatal("project agent disabled state was not persisted")
	}
}

type agentRegistryAdapterForTest struct{ reg *subagent.Registry }

func (a *agentRegistryAdapterForTest) ListConfigs() []tool.AgentConfigInfo {
	configs := a.reg.ListConfigs()
	out := make([]tool.AgentConfigInfo, len(configs))
	for i, config := range configs {
		out[i] = subagent.ToAgentConfigInfo(config)
	}
	return out
}
func (a *agentRegistryAdapterForTest) GetDisabledAt(user bool) map[string]bool {
	return a.reg.GetDisabledAt(user)
}
func (a *agentRegistryAdapterForTest) SetEnabled(name string, enabled, user bool) error {
	return a.reg.SetEnabled(name, enabled, user)
}
