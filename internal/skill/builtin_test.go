package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func TestBuiltinGroup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	previous := Default()
	t.Cleanup(func() { SetDefaultRegistry(previous) })

	skills, err := newLoader(cwd).loadAll()
	if err != nil {
		t.Fatal(err)
	}
	group := skills["group"]
	if group == nil || group.State != StateEnable || !group.DisableModelInvocation || group.Scope != ScopeBuiltin {
		t.Fatalf("unexpected builtin: %+v", group)
	}
	if group.FilePath != "" || group.SkillDir != "" || group.Instructions() == "" {
		t.Fatal("builtin must supply instructions without a disk installation")
	}
	for _, action := range []string{`action="join"`, `action="leave"`, `action="hold"`, "<group>", "/group kick", "/group disband", "/group list", "/group members"} {
		if !strings.Contains(group.Instructions(), action) {
			t.Errorf("instructions missing %q", action)
		}
	}

	Initialize(Options{CWD: cwd})
	r := Default()
	if !r.IsEnabled("group") || len(r.ListEnabled()) != 1 || len(r.ListActive()) != 0 {
		t.Fatal("builtin should be user-visible but not model-visible")
	}
	if !strings.Contains(r.SkillInvocationPrompt("group"), group.Instructions()) {
		t.Fatal("slash invocation must inline builtin instructions")
	}
	if err := r.SetState("group", StateActive, setting.ScopeProject); err != nil {
		t.Fatal(err)
	}
	Initialize(Options{CWD: cwd})
	r = Default()
	loaded, _ := r.Get("group")
	if loaded.State != StateActive || loaded.IsActive() || len(r.ListActive()) != 0 || r.SkillsSection() != "" {
		t.Fatal("persisted active state must not expose user-only skills")
	}
	if group.State != StateEnable {
		t.Fatal("builtin instances must not share state")
	}
}

func TestBuiltinGroupCannotBeShadowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".san", "skills", "group")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: group\n---\nReplacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := newLoader(cwd).loadAll()
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistryForTest(skills, nil, nil)
	r.LoadPersona([]string{dir}, nil)
	group, _ := r.Get("group")
	if group.Scope != ScopeBuiltin || !group.DisableModelInvocation || group.Instructions() != groupInstructions {
		t.Fatal("disk/persona skill shadowed reserved builtin")
	}
	r.ClearPersona()
	if got, _ := r.Get("group"); got != group {
		t.Fatal("clearing persona removed builtin")
	}
}
