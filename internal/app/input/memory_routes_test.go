package input

import (
	"context"
	"os"
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
	result, _, handled := c.Execute(context.Background(), "/reflection")
	if !handled || result != "" || !m.Reflection.IsActive() || m.Memory.Selector.IsActive() {
		t.Fatalf("/reflection did not open the settings popup: %q", result)
	}
	m.Reflection.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if _, _, handled := c.executeBuiltinCommand(context.Background(), "evolve", ""); handled || m.Reflection.IsActive() {
		t.Fatal("/evolve still dispatches as a built-in")
	}
	for _, command := range []string{"/memory", "/memory browse"} {
		result, _, handled := c.Execute(context.Background(), command)
		if !handled || result != "" || !m.Memory.Selector.IsActive() || m.Reflection.IsActive() {
			t.Fatalf("%s did not open the file browser: %q", command, result)
		}
		m.Memory.Selector.Cancel()
	}
	for _, tc := range []struct{ command, want string }{
		{"/memory list", "AGENTS.md"},
		{"/memory show", "Project instructions"},
		{"/memory edit", ""},
		{"/memory off project", "Auto memory off"},
		{"/memory on project", "Auto memory on"},
	} {
		result, _, handled := c.Execute(context.Background(), tc.command)
		if !handled || strings.HasPrefix(result, "Error:") || !strings.Contains(result, tc.want) || m.Reflection.IsActive() {
			t.Fatalf("%s: got %q, want %q and no settings popup", tc.command, result, tc.want)
		}
	}
	if filepath.Base(edited) != "AGENTS.md" || !svc.AutoMemory() {
		t.Fatalf("legacy routes failed: edit=%q memory=%v", edited, svc.AutoMemory())
	}
}

func TestReflectionTogglesScopedMemory(t *testing.T) {
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	t.Setenv("SAN_DISABLE_SELF_LEARN", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	svc := setting.New(setting.NewData())
	c := NewReflectionSelector(ReflectionDeps{Workspace: func() (string, *setting.Settings) { return cwd, svc }})
	c.Enter(120, 40)
	press := func(key rune) { c.HandleKeypress(tea.KeyPressMsg{Code: key}) }
	press(tea.KeyEnter) // User, which defaults to on
	if svc.AutoMemory() || c.ActivePanel().Dirty() {
		t.Fatal("turning Auto Memory off should save immediately")
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
	press(tea.KeyTab)
	for range memoryRowCount {
		press(tea.KeyDown)
	} // Project enablement, which currently inherits user off
	press(tea.KeyEnter)
	user, project := setting.AutoMemoryAt(setting.ScopeUser), setting.AutoMemoryAt(setting.ScopeProject)
	if user == nil || *user || project == nil || !*project || !svc.AutoMemory() {
		t.Fatalf("project toggle should override user off (user=%v project=%v effective=%v)", user, project, svc.AutoMemory())
	}
	for range memoryRowCount {
		press(tea.KeyUp)
	}
	press(tea.KeyEnter)
	user = setting.AutoMemoryAt(setting.ScopeUser)
	if user == nil || !*user || !svc.AutoMemory() {
		t.Fatal("user toggle should flip the user value, not the effective project value")
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
	press(tea.KeyTab)
	c.Enter(120, 40)
	if out := c.Render(); !strings.Contains(out, "effective: off (SAN_DISABLE_AUTO_MEMORY)") {
		t.Fatalf("expected the memory override in the Auto Memory tab: %s", out)
	}
	press(tea.KeyTab)
	if !strings.Contains(c.Render(), "no skill permissions") {
		t.Fatal("the memory override should not change the skill status or its reason")
	}
}
