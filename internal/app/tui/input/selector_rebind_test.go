package input

import (
	"errors"
	"testing"

	coremcp "github.com/genai-io/san/internal/mcp"
	coreplugin "github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/setting"
	coreskill "github.com/genai-io/san/internal/skill"
)

func TestRebindSelectorsSearchWritesReplacementSettings(t *testing.T) {
	tempHome(t)
	old, fresh := setting.New(setting.NewData()), setting.New(setting.NewData())
	m := New(t.TempDir(), 80, nil, SelectorServices{Setting: old})
	m.Search.active = true
	m.Search.items = []searchItem{{Name: "replacement", Available: true}}
	m.RebindSelectors(SelectorServices{Setting: fresh})
	m.Search.Select()
	if fresh.SearchProvider() != "replacement" || old.SearchProvider() == "replacement" {
		t.Fatal("search wrote stale settings")
	}
}

func TestMCPConnectResultIgnoresReplacedManager(t *testing.T) {
	old := coremcp.NewServerSetForTest(nil)
	fresh := coremcp.NewServerSetForTest(map[string]coremcp.ServerConfig{"demo": {Name: "demo"}})
	fresh.SetConnecting("demo", true)
	state := MCPState{Selector: NewMCPSelector(fresh)}
	cmd, handled := UpdateMCP(PanelContext{}, &state, mcpConnectResultMsg{serverSet: old, ServerName: "demo", Err: errors.New("old failure")})
	if !handled || cmd != nil {
		t.Fatal("stale completion not consumed")
	}
	for _, srv := range fresh.List() {
		if srv.Status != coremcp.StatusConnecting {
			t.Fatal("stale completion modified current manager")
		}
	}
	// Connect results retain their source manager.
	msg := mcpStartConnect(old, "missing")().(mcpConnectResultMsg)
	if msg.serverSet != old {
		t.Fatal("connect command omitted manager identity")
	}
}

func TestRebindSelectorsPreservesInputAndSamePlugin(t *testing.T) {
	tempHome(t)
	reg := coreplugin.NewRegistry()
	deps := SelectorServices{PluginRegistry: reg}
	m := New(t.TempDir(), 80, nil, deps)
	m.Textarea.SetValue("draft")
	m.Queue.Enqueue("queued", nil)
	m.History.Stashed = "history draft"
	m.Skill.SetPending("pending", "instructions")
	m.Settings.Enter(100, 40)
	m.Settings.index = 2
	m.Plugin.active = true
	m.Plugin.isLoading = true
	m.Plugin.loadingMsg = "installing"
	m.Plugin.activeTab = pluginTab(1)
	reflection := m.Reflection.panels[0]
	m.RebindSelectors(deps)
	if m.Textarea.Value() != "draft" || m.Queue.Len() != 1 || m.History.Stashed != "history draft" || m.Skill.PendingFullName != "pending" {
		t.Fatal("reload destroyed input state")
	}
	if !m.Settings.active || m.Settings.index != 2 {
		t.Fatal("reload reset settings popup")
	}
	if !m.Plugin.active || !m.Plugin.isLoading || m.Plugin.loadingMsg != "installing" || m.Plugin.activeTab != pluginTab(1) {
		t.Fatal("same plugin registry lost operation state")
	}
	if m.Reflection.panels[0] != reflection {
		t.Fatal("reload replaced the Reflection popup")
	}
}

func TestRebindSelectorsToolUsesReplacementCallbacks(t *testing.T) {
	tempHome(t)
	for _, open := range []bool{false, true} {
		t.Run(map[bool]string{false: "inactive", true: "open"}[open], func(t *testing.T) {
			oldSaves, newSaves := 0, 0
			deps := SelectorServices{LoadDisabled: func(setting.Scope) map[string]bool { return map[string]bool{"demo": true} }, UpdateDisabled: func(string, *bool, setting.Scope) error { oldSaves++; return nil }}
			m := New(t.TempDir(), 80, nil, deps)
			if open {
				_ = m.Tool.EnterSelect(100, 40, nil)
			}
			deps.LoadDisabled = func(setting.Scope) map[string]bool { return map[string]bool{"demo": false} }
			deps.UpdateDisabled = func(name string, disabled *bool, _ setting.Scope) error {
				newSaves++
				if name != "demo" || disabled == nil || !*disabled {
					t.Error("toggle used stale disabled cache")
				}
				return nil
			}
			deps.HeldOn = func() map[string]string { return map[string]string{"demo": "current"} }
			m.RebindSelectors(deps)
			if !open {
				_ = m.Tool.EnterSelect(100, 40, nil)
			}
			m.Tool.list.load([]toolItem{{Name: "demo"}}, 100, 40)
			m.Tool.Toggle()
			if oldSaves != 0 || newSaves != 1 || m.Tool.held["demo"] != "current" {
				t.Fatal("tool used old callbacks")
			}
		})
	}
}

func TestRebindSelectorsSkillReplacementClosesCacheAndReopensFresh(t *testing.T) {
	tempHome(t)
	userStore, err := coreskill.NewStore(t.TempDir() + "/user.json")
	if err != nil {
		t.Fatal(err)
	}
	projectStore, err := coreskill.NewStore(t.TempDir() + "/project.json")
	if err != nil {
		t.Fatal(err)
	}
	old := coreskill.NewRegistryForTest(map[string]*coreskill.Skill{"old": {Name: "old"}}, userStore, projectStore)
	fresh := coreskill.NewRegistryForTest(map[string]*coreskill.Skill{"fresh": {Name: "fresh"}}, userStore, projectStore)
	m := New(t.TempDir(), 80, nil, SelectorServices{SkillRegistry: old})
	if err := m.Skill.Selector.EnterSelect(100, 40); err != nil {
		t.Fatal(err)
	}
	m.Skill.SetPending("old", "pending")
	m.RebindSelectors(SelectorServices{SkillRegistry: fresh})
	if m.Skill.Selector.IsActive() {
		t.Fatal("stale skill selector remains open")
	}
	if err := m.Skill.Selector.EnterSelect(100, 40); err != nil {
		t.Fatal(err)
	}
	if len(m.Skill.Selector.list.items) != 1 || m.Skill.Selector.list.items[0].Name != "fresh" {
		t.Fatal("reopen used stale registry")
	}
	if m.Skill.PendingFullName != "old" {
		t.Fatal("lost pending invocation")
	}
}
