package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

func TestFailedReviewSkillResultDoesNotRequestAutoSkill(t *testing.T) {
	m := &model{services: services{
		Tool: tool.NewRegistry(),
		Hook: hook.NewEngine(setting.NewData(), "", t.TempDir(), ""),
	}}

	m.OnToolResult(core.ToolResult{ToolName: tool.ToolReviewSkill, IsError: true})
	if m.skillReviewRequested {
		t.Fatal("failed ReviewSkill result requested autonomous learning")
	}

	m.OnToolResult(core.ToolResult{ToolName: tool.ToolReviewSkill})
	if !m.skillReviewRequested {
		t.Fatal("successful ReviewSkill result did not request learning")
	}
}

func TestAutoSkillCapabilitiesHonorHardDisable(t *testing.T) {
	m := &model{services: services{Setting: &setting.Settings{}}}

	t.Setenv("SAN_DISABLE_SELF_LEARN", "")
	if caps := m.autoSkillCapabilities(); !caps.Active() {
		t.Fatal("default skill permissions should advertise ReviewSkill")
	}

	t.Setenv("SAN_DISABLE_SELF_LEARN", "1")
	if caps := m.autoSkillCapabilities(); caps.Active() {
		t.Fatalf("hard-disabled self-learning advertised ReviewSkill: %+v", caps)
	}
}

func TestAutoSkillCapabilitiesRequireAutoMemory(t *testing.T) {
	t.Setenv("SAN_DISABLE_SELF_LEARN", "")
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "1")
	m := &model{services: services{Setting: &setting.Settings{}}}
	if caps := m.autoSkillCapabilities(); caps.Active() {
		t.Fatalf("Auto Memory disabled but ReviewSkill advertised: %+v", caps)
	}
}

func TestAutoSkillCapabilitiesRejectInvalidSettings(t *testing.T) {
	t.Setenv("SAN_TRUST_WORKSPACE", "1") // project settings apply in a trusted folder
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestFile(t, filepath.Join(cwd, ".san", "settings.json"),
		`{"autoSkill":{"skills":{"enabled":true,"denyUpdate":true}}}`)

	settings := &setting.Settings{}
	if err := settings.Reload(cwd); err != nil {
		t.Fatal(err)
	}
	m := &model{services: services{Setting: settings}}
	if caps := m.autoSkillCapabilities(); caps.Active() {
		t.Fatalf("invalid settings advertised ReviewSkill: %+v", caps)
	}
}

func TestNotifyAutoSkillOverrideChecksIndividualSkillActions(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestFile(t, filepath.Join(home, ".san", "settings.json"),
		`{"autoSkill":{"skills":{"enabled":true,"denyCreate":true}}}`)
	writeTestFile(t, filepath.Join(cwd, ".san", "settings.json"),
		`{"autoSkill":{"skills":{"enabled":true}}}`)

	settings := &setting.Settings{}
	if err := settings.Reload(cwd); err != nil {
		t.Fatal(err)
	}
	m := &model{
		services: services{Setting: settings},
		conv:     conv.NewModel(80),
	}
	m.notifyAutoSkillOverride(input.ConfigSavedMsg{
		Scope:          setting.ScopeProject,
		SavedAutoSkill: setting.AutoSkillSettings{Skills: setting.AutoSkillPermissions{}},
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
		`{"autoSkill":{"skills":{"enabled":true}}}`)
	writeTestFile(t, filepath.Join(cwdB, ".san", "settings.json"),
		`{"autoSkill":{"skills":{"enabled":true}}}`)
	writeTestFile(t, filepath.Join(cwdA, ".san", "skills", "skill-a", "SKILL.md"),
		"---\ndescription: from a\norigin: agent-created\n---\n# A\n")
	writeTestFile(t, filepath.Join(cwdB, ".san", "skills", "skill-b", "SKILL.md"),
		"---\ndescription: from b\norigin: agent-created\n---\n# B\n")

	settingsA := &setting.Settings{}
	if err := settingsA.Reload(cwdA); err != nil {
		t.Fatal(err)
	}
	source := newLearnedStoreContext(cwdA, settingsA)
	skills := newLearnedSkillStore(source.Snapshot)
	assertLearnedSkillNames(t, skills, "skill-a")

	settingsB := &setting.Settings{}
	if err := settingsB.Reload(cwdB); err != nil {
		t.Fatal(err)
	}
	source.Update(cwdB, settingsB)
	assertLearnedSkillNames(t, skills, "skill-b")
}

func assertLearnedSkillNames(t *testing.T, skills input.LearnedSkillStore, wantSkill string) {
	t.Helper()
	gotSkills := skills.List()
	if len(gotSkills) != 1 || gotSkills[0].Name != wantSkill {
		t.Fatalf("learned skills = %+v, want %q", gotSkills, wantSkill)
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
