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
	m := &Model{MemorySkills: NewMemorySkillsSelector(MemorySkillsDeps{
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
	result, _, handled := c.Execute(context.Background(), "/memory")
	if !handled || result != "" || !m.MemorySkills.IsActive() || m.Memory.Selector.IsActive() {
		t.Fatalf("/memory did not open Memory & Skills: %q", result)
	}
	m.MemorySkills.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if _, _, handled := c.executeBuiltinCommand(context.Background(), "evolve", ""); handled || m.MemorySkills.IsActive() {
		t.Fatal("/evolve still dispatches as a built-in")
	}
	for _, tc := range []struct{ command, want string }{
		{"/memory list", "AGENTS.md"},
		{"/memory show", "Project instructions"},
		{"/memory edit", ""},
		{"/memory off project", "Auto memory off"},
		{"/memory on project", "Auto memory on"},
		{"/memory browse", ""},
	} {
		result, _, handled := c.Execute(context.Background(), tc.command)
		if !handled || strings.HasPrefix(result, "Error:") || !strings.Contains(result, tc.want) || m.MemorySkills.IsActive() {
			t.Fatalf("%s: got %q, want %q and no settings popup", tc.command, result, tc.want)
		}
	}
	if filepath.Base(edited) != "AGENTS.md" || !m.Memory.Selector.IsActive() || !svc.AutoMemory() {
		t.Fatalf("legacy routes failed: edit=%q browse=%v memory=%v", edited, m.Memory.Selector.IsActive(), svc.AutoMemory())
	}
}

func TestLearningPanelTogglesScopedMemory(t *testing.T) {
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	svc := setting.New(setting.NewData())
	c := NewMemorySkillsSelector(MemorySkillsDeps{Workspace: func() (string, *setting.Settings) { return cwd, svc }})
	c.Enter(120, 40)
	press := func(key rune) { c.HandleKeypress(tea.KeyPressMsg{Code: key}) }
	if p := c.ActivePanel().(*memorySkillsPanel); p.skillStatus(nil) != "on" {
		t.Fatal("Auto Skill should be active with default permissions and memory")
	}
	for range 6 { // Strategy → Create → Update → Delete → Learned → Save → User
		press(tea.KeyDown)
	}
	press(tea.KeyEnter)
	if svc.AutoMemory() || !strings.Contains(c.Render(), "requires Auto Memory") || c.ActivePanel().Dirty() {
		t.Fatal("user toggle should save immediately and show Auto Skill dependency")
	}
	press(tea.KeyDown) // Project, which currently inherits user off
	press(tea.KeyEnter)
	user, project := setting.AutoMemoryAt(setting.ScopeUser), setting.AutoMemoryAt(setting.ScopeProject)
	if user == nil || *user || project == nil || !*project || !svc.AutoMemory() {
		t.Fatalf("project toggle should override user off (user=%v project=%v effective=%v)", user, project, svc.AutoMemory())
	}
	press(tea.KeyUp)
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
	c.Enter(120, 40)
	if !strings.Contains(c.Render(), "no skill permissions") {
		t.Fatal("Auto Skill should be off when all effective skill permissions are denied")
	}

	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "1")
	c.Enter(120, 40)
	if out := c.Render(); !strings.Contains(out, "effective: off (SAN_DISABLE_AUTO_MEMORY)") || !strings.Contains(out, "requires Auto Memory") {
		t.Fatal("environment override should be shown in both settings groups")
	}
}
