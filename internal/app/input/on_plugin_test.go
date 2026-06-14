package input

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
	coreplugin "github.com/genai-io/san/internal/plugin"
)

func TestCancelClearsTransientPluginSelectorState(t *testing.T) {
	m := NewPluginSelector(coreplugin.NewRegistry())
	m.active = true
	m.level = pluginLevelDetail
	m.selectedIdx = 3
	m.scrollOffset = 4
	m.parentIdx = 2
	m.searchQuery = "plug"
	m.filteredItems = []any{"plugin"}
	m.detailPlugin = &pluginItem{Name: "demo"}
	m.detailDiscover = &pluginDiscoverItem{Name: "discover"}
	m.detailMarketplace = &pluginMarketplaceItem{ID: "market"}
	m.actions = []pluginAction{{Label: "Back", Action: "back"}}
	m.actionIdx = 1
	m.addMarketplaceInput = "owner/repo"
	m.addDialogCursor = 1
	m.browseMarketplaceID = "market"
	m.browsePlugins = []pluginDiscoverItem{{Name: "child"}}
	m.isLoading = true
	m.loadingMsg = "loading"
	m.lastMessage = "oops"
	m.isError = true

	m.Cancel()

	if m.active {
		t.Fatal("Cancel should deactivate selector")
	}
	if m.level != pluginLevelTabList || m.selectedIdx != 0 || m.scrollOffset != 0 || m.parentIdx != 0 {
		t.Fatal("Cancel should reset list navigation state")
	}
	if m.searchQuery != "" || m.filteredItems != nil {
		t.Fatal("Cancel should clear search state")
	}
	if m.detailPlugin != nil || m.detailDiscover != nil || m.detailMarketplace != nil || m.actions != nil || m.actionIdx != 0 {
		t.Fatal("Cancel should clear detail state")
	}
	if m.addMarketplaceInput != "" || m.addDialogCursor != 0 {
		t.Fatal("Cancel should clear add-marketplace input state")
	}
	if m.browseMarketplaceID != "" || m.browsePlugins != nil {
		t.Fatal("Cancel should clear browse state")
	}
	if m.isLoading || m.loadingMsg != "" {
		t.Fatal("Cancel should clear loading state")
	}
	if m.lastMessage != "" || m.isError {
		t.Fatal("Cancel should clear status message")
	}
}

func TestHandleListEscClearsSearchBeforeDismiss(t *testing.T) {
	m := NewPluginSelector(coreplugin.NewRegistry())
	m.active = true
	m.activeTab = pluginTabInstalled
	m.installedFlatList = []pluginItem{
		{Name: "alpha"},
		{Name: "beta"},
	}
	m.searchQuery = "alp"
	m.filteredItems = []any{m.installedFlatList[0]}

	cmd := m.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})

	if cmd != nil {
		t.Fatal("Esc with active search should only clear search")
	}
	if m.searchQuery != "" {
		t.Fatalf("searchQuery = %q, want empty", m.searchQuery)
	}
	if len(m.filteredItems) != len(m.installedFlatList) {
		t.Fatal("clearing search should restore full filtered list")
	}
	if !m.active {
		t.Fatal("clearing search should not dismiss selector")
	}
}

func TestHandleListEscDismissesSelector(t *testing.T) {
	m := NewPluginSelector(coreplugin.NewRegistry())
	m.active = true

	cmd := m.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("Esc should return dismiss command when no search is active")
	}
	msg := cmd()
	if _, ok := msg.(kit.DismissedMsg); !ok {
		t.Fatalf("dismiss command returned %T, want kit.DismissedMsg", msg)
	}
	if m.active {
		t.Fatal("dismiss should deactivate selector")
	}
}

func TestSwitchTabResetsDetailStateAndSearch(t *testing.T) {
	m := NewPluginSelector(coreplugin.NewRegistry())
	m.activeTab = pluginTabInstalled
	m.level = pluginLevelDetail
	m.selectedIdx = 3
	m.scrollOffset = 2
	m.parentIdx = 1
	m.searchQuery = "demo"
	m.detailPlugin = &pluginItem{Name: "demo"}
	m.actions = []pluginAction{{Label: "Back", Action: "back"}}
	m.marketplaces = []pluginMarketplaceItem{{ID: "market"}}

	m.switchTab(pluginTabMarketplaces)

	if m.activeTab != pluginTabMarketplaces {
		t.Fatalf("activeTab = %v, want pluginTabMarketplaces", m.activeTab)
	}
	if m.level != pluginLevelTabList || m.selectedIdx != 0 || m.scrollOffset != 0 || m.parentIdx != 0 {
		t.Fatal("switchTab should reset list navigation")
	}
	if m.searchQuery != "" {
		t.Fatal("switchTab should clear search query")
	}
	if m.detailPlugin != nil || m.actions != nil || m.actionIdx != 0 {
		t.Fatal("switchTab should clear detail state")
	}
	if len(m.filteredItems) != len(m.marketplaces) {
		t.Fatal("switchTab should refresh filtered items for the active tab")
	}
}

func TestToggleSelectedPluginReturnsDisableMsg(t *testing.T) {
	m := NewPluginSelector(coreplugin.NewRegistry())
	m.activeTab = pluginTabInstalled
	m.level = pluginLevelTabList
	m.filteredItems = []any{
		pluginItem{
			Name:     "demo",
			FullName: "demo",
			Enabled:  true,
			Scope:    coreplugin.ScopeUser,
		},
	}

	cmd := m.toggleSelectedPlugin()
	if cmd == nil {
		t.Fatal("toggleSelectedPlugin should return a command")
	}
	msg := cmd()
	if disable, ok := msg.(PluginDisableMsg); !ok || disable.PluginName != "demo" {
		t.Fatalf("unexpected toggle message: %#v", msg)
	}
}

func TestRenderTabListShowsPluginManagerFrame(t *testing.T) {
	m := NewPluginSelector(coreplugin.NewRegistry())
	m.active = true
	m.width = 100
	m.height = 30
	m.activeTab = pluginTabInstalled
	m.installedFlatList = []pluginItem{{Name: "demo", Description: "demo plugin"}}
	m.filteredItems = []any{m.installedFlatList[0]}

	rendered := m.Render()
	for _, want := range []string{"Discover", "Installed", "Marketplaces", "filter"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Render() missing %q in output:\n%s", want, rendered)
		}
	}
}

func TestRenderInstalledDetailShowsStructuredSections(t *testing.T) {
	m := NewPluginSelector(coreplugin.NewRegistry())
	m.active = true
	m.width = 100
	m.height = 30
	m.level = pluginLevelDetail
	m.detailPlugin = &pluginItem{
		Name:        "deploy",
		FullName:    "deploy@corp",
		Description: "Deploy safely",
		Enabled:     true,
		Scope:       coreplugin.ScopeProject,
		Skills:      1,
		Agents:      2,
		Hooks:       1,
	}
	m.actions = []pluginAction{{Label: "Disable plugin", Action: "disable"}, {Label: "Back", Action: "back"}}

	rendered := m.Render()
	for _, want := range []string{"Plugin Details", "deploy@corp", "Status:", "Scope:", "Components", "Disable plugin"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Render() missing %q in output:\n%s", want, rendered)
		}
	}
}

func TestHandlePluginCommandMarketplaceAddListRemove(t *testing.T) {
	reg := coreplugin.NewRegistry()
	coreplugin.SetDefaultRegistry(reg)
	t.Cleanup(coreplugin.ResetDefaultRegistry)

	tmpHome := t.TempDir()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpHome)

	marketplaceDir := createTestPluginMarketplace(t)
	selector := NewPluginSelector(reg)

	result, err := HandlePluginCommand(context.Background(), &selector, tmpDir, 80, 24, "marketplace add "+marketplaceDir+" openai-codex")
	if err != nil {
		t.Fatalf("HandlePluginCommand(add) error = %v", err)
	}
	if !strings.Contains(result, "Added marketplace 'openai-codex'.") {
		t.Fatalf("unexpected add result: %q", result)
	}

	result, err = HandlePluginCommand(context.Background(), &selector, tmpDir, 80, 24, "marketplace list")
	if err != nil {
		t.Fatalf("HandlePluginCommand(list) error = %v", err)
	}
	if !strings.Contains(result, "openai-codex (directory)") {
		t.Fatalf("list result missing marketplace entry: %q", result)
	}
	if !strings.Contains(result, marketplaceDir) {
		t.Fatalf("list result missing marketplace path: %q", result)
	}

	result, err = HandlePluginCommand(context.Background(), &selector, tmpDir, 80, 24, "marketplace remove openai-codex")
	if err != nil {
		t.Fatalf("HandlePluginCommand(remove) error = %v", err)
	}
	if !strings.Contains(result, "Removed marketplace 'openai-codex'.") {
		t.Fatalf("unexpected remove result: %q", result)
	}
}

func TestHandlePluginCommandInstallFromMarketplace(t *testing.T) {
	reg := coreplugin.NewRegistry()
	coreplugin.SetDefaultRegistry(reg)
	t.Cleanup(coreplugin.ResetDefaultRegistry)

	tmpHome := t.TempDir()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpHome)

	marketplaceDir := createTestPluginMarketplace(t)
	selector := NewPluginSelector(reg)

	if _, err := HandlePluginCommand(context.Background(), &selector, tmpDir, 80, 24, "marketplace add "+marketplaceDir+" openai-codex"); err != nil {
		t.Fatalf("HandlePluginCommand(add) error = %v", err)
	}

	result, err := HandlePluginCommand(context.Background(), &selector, tmpDir, 80, 24, "install codex@openai-codex")
	if err != nil {
		t.Fatalf("HandlePluginCommand(install) error = %v", err)
	}
	if !strings.Contains(result, "Installed plugin 'codex@openai-codex'") {
		t.Fatalf("unexpected install result: %q", result)
	}
	if !strings.Contains(result, "/reload-plugins") {
		t.Fatalf("install result should mention reload command: %q", result)
	}

	if _, ok := reg.Get("codex@openai-codex"); !ok {
		t.Fatal("expected installed plugin to be registered")
	}
}

func createTestPluginMarketplace(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins", "codex", ".san-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", pluginDir, err)
	}

	manifest := `{"name":"codex","description":"Test marketplace plugin"}`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile(plugin.json): %v", err)
	}

	return root
}
