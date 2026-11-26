package input

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/setting"
)

func TestMemorySlashRoutes(t *testing.T) {
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	svc := setting.New(setting.NewData())
	m := &Model{Reflection: NewReflectionSelector(ReflectionDeps{
		Workspace: func() (string, *setting.Settings) { return cwd, svc },
	})}
	path := filepath.Join(cwd, "AGENTS.md")
	if err := os.WriteFile(path, []byte("Project instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	var edited string
	c := NewSlashCommandController(SlashCommandEnv{
		Input: m, Cwd: cwd, Setting: svc, Width: 120, Height: 40,
		StartExternalEditor: func(path string) tea.Cmd { edited = path; return nil },
	})
	for _, name := range []string{"/reflect", "/reflection"} {
		result, _, handled := c.Execute(context.Background(), name)
		if !handled || result != "" || !m.Reflection.IsActive() || m.Memory.Selector.IsActive() {
			t.Fatalf("%s did not open the settings popup: %q", name, result)
		}
		m.Reflection.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	}
	if _, _, handled := c.executeBuiltinCommand(context.Background(), "evolve", ""); handled || m.Reflection.IsActive() {
		t.Fatal("/evolve still dispatches as a built-in")
	}
	for _, command := range []string{"/memory", "/memory browse"} {
		result, _, handled := c.Execute(context.Background(), command)
		if !handled || result != "" || !m.Memory.Selector.IsActive() || m.Reflection.IsActive() {
			t.Fatalf("%s did not open the file browser: %q", command, result)
		}
		items := m.Memory.Selector.items
		if len(items) != 4 || !items[3].isAuto() || items[3].Label != "Store" {
			t.Fatalf("want three instruction rows and one Auto Memory store: %+v", items)
		}
		m.Memory.Selector.Cancel()
	}
	for _, tc := range []struct{ command, want string }{
		{"/memory list", "AGENTS.md"},
		{"/memory show", "Project instructions"},
		{"/memory edit", ""},
		{"/memory off", "Auto memory off"},
		{"/memory on", "Auto memory on"},
	} {
		result, _, handled := c.Execute(context.Background(), tc.command)
		if !handled || strings.HasPrefix(result, "Error:") || !strings.Contains(result, tc.want) || m.Reflection.IsActive() {
			t.Fatalf("%s: got %q, want %q and no settings popup", tc.command, result, tc.want)
		}
	}
	if filepath.Base(edited) != "AGENTS.md" || !svc.AutoMemory() {
		t.Fatalf("instruction edit or Auto Memory toggle failed: edit=%q memory=%v", edited, svc.AutoMemory())
	}
	if user, project := setting.AutoMemoryAt(cwd, setting.ScopeUser), setting.AutoMemoryAt(cwd, setting.ScopeProject); user == nil || !*user || project != nil {
		t.Fatal("outside a repository /memory on should save only the user default")
	}
	if result, _, _ := c.Execute(context.Background(), "/memory off project"); !strings.Contains(result, "usage: /memory off") || !svc.AutoMemory() {
		t.Fatalf("obsolete scope argument should not change the setting: %q", result)
	}
}

func TestMemoryBrowserChangesOnlyRepositoryDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	m := NewMemorySelector()
	m.EnterSelect(cwd, setting.New(nil), 100, 30)
	m.selectedIdx = len(m.items) - 1
	if strings.Contains(m.Render(), "change dir") {
		t.Fatal("directory hint should be unavailable outside a repository")
	}
	m.openDirInput()
	if m.editingDir || m.err == nil {
		t.Fatal("directory editing should be unavailable outside a repository")
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	m.loadItems()
	if !strings.Contains(m.Render(), "change dir") {
		t.Fatal("directory hint should refresh when the repository is loaded")
	}
	m.openDirInput()
	if !m.editingDir || m.err != nil {
		t.Fatalf("directory editor did not open: %v", m.err)
	}
	to := filepath.Join(t.TempDir(), "notes")
	m.dirInput.SetValue(to)
	m.handleDirInput(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.editingDir || m.err != nil || m.items[m.selectedIdx].Path != filepath.Join(to, "MEMORY.md") {
		t.Fatalf("directory change not reflected in the browser: item=%+v, err=%v", m.items[m.selectedIdx], m.err)
	}
}

func TestReflectionTogglesRepositoryMemory(t *testing.T) {
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	t.Setenv("SAN_DISABLE_SELF_LEARN", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := setting.SaveAutoMemory(cwd, true, setting.ScopeUser); err != nil {
		t.Fatal(err)
	}
	svc := setting.New(setting.NewData())
	c := NewReflectionSelector(ReflectionDeps{Workspace: func() (string, *setting.Settings) { return cwd, svc }})
	c.Enter(120, 40)
	press := func(key rune) { c.HandleKeypress(tea.KeyPressMsg{Code: key}) }
	press(tea.KeyRight) // Options.
	press(tea.KeyEnter) // Pause the library without changing Recall mode.
	press(tea.KeyEscape)
	user, project := setting.AutoMemoryAt(cwd, setting.ScopeUser), setting.AutoMemoryAt(cwd, setting.ScopeProject)
	if user == nil || !*user || project == nil || *project || svc.AutoMemory() || c.ActivePanel().Dirty() {
		t.Fatal("turning Auto Memory off should save a repository override immediately and leave the user default on")
	}
	press(tea.KeyTab)
	p := c.ActivePanel().(*autoSkillPanel)
	if got := p.skillStatus(); got != "off · ReviewSkill disabled" {
		t.Fatalf("default Auto Skill status = %q", got)
	}
	if err := setting.UpdateAutoSkillAt(setting.AutoSkillSettings{}, setting.ScopeUser); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(cwd); err != nil {
		t.Fatal(err)
	}
	if got := p.skillStatus(); got != "on" {
		t.Fatalf("Auto Skill can run with Auto Memory off; status = %q", got)
	}
	t.Setenv("SAN_DISABLE_SELF_LEARN", "1")
	if got := p.skillStatus(); got != "off · SAN_DISABLE_SELF_LEARN" {
		t.Fatalf("hard-disabled Auto Skill status = %q", got)
	}
	t.Setenv("SAN_DISABLE_SELF_LEARN", "")
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) // Auto Skill → Auto Memory
	press(tea.KeyRight)
	press(tea.KeyEnter)
	press(tea.KeyEscape)
	project = setting.AutoMemoryAt(cwd, setting.ScopeProject)
	if project == nil || !*project || !svc.AutoMemory() {
		t.Fatal("the same toggle should turn the repository override back on")
	}

	if err := setting.UpdateAutoSkillAt(setting.AutoSkillSettings{
		Skills: setting.AutoSkillPermissions{DenyCreate: true, DenyUpdate: true, DenyDelete: true},
	}, setting.ScopeUser); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(cwd); err != nil {
		t.Fatal(err)
	}
	press(tea.KeyTab)
	c.Enter(120, 40)
	if !strings.Contains(c.Render(), "no skill permissions") {
		t.Fatal("Auto Skill should be off when all effective skill permissions are denied")
	}

	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "1")
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) // Auto Skill → Auto Memory
	c.Enter(120, 40)
	if out := c.Render(); !strings.Contains(out, "Paused · SAN_DISABLE_AUTO_MEMORY") {
		t.Fatalf("expected the memory override in the Auto Memory tab: %s", out)
	}
	press(tea.KeyTab)
	if !strings.Contains(c.Render(), "no skill permissions") {
		t.Fatal("the memory override should not change the skill status or its reason")
	}
}
