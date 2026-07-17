package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

func TestFailedEvolveResultDoesNotRequestSelfLearn(t *testing.T) {
	m := &model{services: services{
		Tool: tool.NewRegistry(),
		Hook: hook.NewEngine(setting.NewData(), "", t.TempDir(), ""),
	}}

	m.OnToolResult(core.ToolResult{ToolName: tool.ToolEvolve, IsError: true})
	if m.evolveRequestedThisTurn {
		t.Fatal("failed Evolve result requested autonomous learning")
	}

	m.OnToolResult(core.ToolResult{ToolName: tool.ToolEvolve})
	if !m.evolveRequestedThisTurn {
		t.Fatal("successful Evolve result did not request learning")
	}
}

func TestSelfLearnCapabilitiesHonorHardDisable(t *testing.T) {
	m := &model{services: services{Setting: &setting.Settings{}}}

	t.Setenv("SAN_DISABLE_SELF_LEARN", "")
	if caps := m.selfLearnCapabilities(); !caps.Active() {
		t.Fatal("default skill permissions should advertise Evolve")
	}

	t.Setenv("SAN_DISABLE_SELF_LEARN", "1")
	if caps := m.selfLearnCapabilities(); caps.Active() {
		t.Fatalf("hard-disabled self-learning advertised Evolve: %+v", caps)
	}
}

func TestSelfLearnCapabilitiesRejectInvalidSettings(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestFile(t, filepath.Join(cwd, ".san", "settings.json"),
		`{"selfLearn":{"skills":{"enabled":true,"denyUpdate":true}}}`)

	settings := &setting.Settings{}
	if err := settings.Reload(cwd); err != nil {
		t.Fatal(err)
	}
	m := &model{services: services{Setting: settings}}
	if caps := m.selfLearnCapabilities(); caps.Active() {
		t.Fatalf("invalid settings advertised Evolve: %+v", caps)
	}
}

func TestNotifySelfLearnOverrideChecksIndividualSkillActions(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestFile(t, filepath.Join(home, ".san", "settings.json"),
		`{"selfLearn":{"skills":{"enabled":true,"denyCreate":true}}}`)
	writeTestFile(t, filepath.Join(cwd, ".san", "settings.json"),
		`{"selfLearn":{"skills":{"enabled":true}}}`)

	settings := &setting.Settings{}
	if err := settings.Reload(cwd); err != nil {
		t.Fatal(err)
	}
	m := &model{
		services: services{Setting: settings},
		conv:     conv.NewModel(80),
	}
	m.notifySelfLearnOverride(input.ConfigSavedMsg{
		Scope:          "project",
		SavedSelfLearn: setting.SelfLearnSettings{Skills: setting.SelfLearnSkills{}},
	})

	if len(m.conv.Messages) != 1 || !strings.Contains(m.conv.Messages[0].Content, "Skill create") {
		t.Fatalf("per-action override notice missing: %+v", m.conv.Messages)
	}
}

func TestLearnedStoresFollowLiveWorkspace(t *testing.T) {
	home := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeTestFile(t, filepath.Join(cwdA, ".san", "settings.json"),
		`{"selfLearn":{"memory":{"path":"memory-a"},"skills":{"enabled":true}}}`)
	writeTestFile(t, filepath.Join(cwdB, ".san", "settings.json"),
		`{"selfLearn":{"memory":{"path":"memory-b"},"skills":{"enabled":true}}}`)
	writeTestFile(t, filepath.Join(cwdA, ".san", "skills", "skill-a", "SKILL.md"),
		"---\ndescription: from a\norigin: agent-created\n---\n# A\n")
	writeTestFile(t, filepath.Join(cwdB, ".san", "skills", "skill-b", "SKILL.md"),
		"---\ndescription: from b\norigin: agent-created\n---\n# B\n")
	writeTestFile(t, filepath.Join(cwdA, "memory-a", system.AutoMemoryIndexName), "from a\n")
	writeTestFile(t, filepath.Join(cwdB, "memory-b", "topic-b.md"), "from b\n")

	settingsA := &setting.Settings{}
	if err := settingsA.Reload(cwdA); err != nil {
		t.Fatal(err)
	}
	source := newLearnedStoreContext(cwdA, settingsA)
	skills := newLearnedSkillStore(source.Snapshot)
	memory := newLearnedMemoryStore(source.Snapshot)
	assertLearnedStoreNames(t, skills, memory, "skill-a", system.AutoMemoryIndexName)

	settingsB := &setting.Settings{}
	if err := settingsB.Reload(cwdB); err != nil {
		t.Fatal(err)
	}
	source.Update(cwdB, settingsB)
	assertLearnedStoreNames(t, skills, memory, "skill-b", "topic-b.md")
}

func assertLearnedStoreNames(t *testing.T, skills input.LearnedSkillStore, memory input.LearnedMemoryStore, wantSkill, wantMemory string) {
	t.Helper()
	gotSkills := skills.List()
	if len(gotSkills) != 1 || gotSkills[0].Name != wantSkill {
		t.Fatalf("learned skills = %+v, want %q", gotSkills, wantSkill)
	}
	gotMemory := memory.List()
	if len(gotMemory) != 1 || gotMemory[0].File != wantMemory {
		t.Fatalf("learned memory = %+v, want %q", gotMemory, wantMemory)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
