package tui

import (
	"context"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/app/tui/transcript"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
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
	data := setting.NewData()
	data.DisabledTools[tool.ToolReviewSkill] = false
	m := &model{services: services{Setting: setting.New(data)}}

	t.Setenv("SAN_DISABLE_SELF_LEARN", "")
	if caps := m.autoSkillCapabilities(); !caps.Active() {
		t.Fatal("enabled ReviewSkill should advertise the allowed actions")
	}

	t.Setenv("SAN_DISABLE_SELF_LEARN", "1")
	if caps := m.autoSkillCapabilities(); caps.Active() {
		t.Fatalf("hard-disabled self-learning advertised ReviewSkill: %+v", caps)
	}
}

func TestAutoSkillAndAutoMemoryEnableIndependently(t *testing.T) {
	t.Setenv("SAN_DISABLE_SELF_LEARN", "")
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	for _, memoryOn := range []bool{false, true} {
		for _, skillOn := range []bool{false, true} {
			data := setting.NewData()
			data.DisabledTools[tool.ToolReviewSkill] = false
			data.AutoMemory = &memoryOn
			data.AutoSkill.Skills = setting.AutoSkillPermissions{
				DenyCreate: !skillOn, DenyUpdate: !skillOn, DenyDelete: !skillOn,
			}
			svc := setting.New(data)
			m := &model{services: services{Setting: svc}}
			if gotMemory, gotSkill := svc.AutoMemory(), len(m.autoSkillExtraTools()) != 0; gotMemory != memoryOn || gotSkill != skillOn {
				t.Fatalf("Auto Memory = %v, Auto Skill = %v; want memory = %v, skill = %v", gotMemory, gotSkill, memoryOn, skillOn)
			}
		}
	}

	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "1")
	data := setting.NewData()
	data.DisabledTools[tool.ToolReviewSkill] = false
	m := &model{services: services{Setting: setting.New(data)}}
	if m.services.Setting.AutoMemory() || len(m.autoSkillExtraTools()) == 0 {
		t.Fatal("forcing Auto Memory off should not disable Auto Skill")
	}
}

func TestAutoSkillRechecksAuthorizationBeforeWriting(t *testing.T) {
	t.Setenv("SAN_DISABLE_SELF_LEARN", "")
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	m, _ := resumedModel(t, nil)
	settingsPath := filepath.Join(m.env.CWD, ".san", "settings.json")
	writeTestFile(t, settingsPath, `{"disabledTools":{"ReviewSkill":false}}`)
	if err := m.services.Setting.Reload(m.env.CWD); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	reply := aitest.Says(`{"action":"create","name":"test-workflow","level":"project","description":"Test workflow","body":"Use table tests.","note":"save workflow"}`)
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		close(started)
		select {
		case <-release:
			return reply(ctx, req)
		case <-ctx.Done():
			return aitest.Fails(ctx.Err())(ctx, req)
		}
	})
	params := agent.BuildParams{Provider: &finalizationProvider{client: driver.Client()}, ModelID: "m",
		Recording: m.services.Session, PermissionRules: (agent.PermissionPolicy{}).Decide}
	if err := m.services.Agent.Start(params, nil); err != nil {
		t.Fatal(err)
	}
	m.wireAutoSkill(params)
	reviewer := m.services.AutoSkill.session
	if reviewer == nil {
		t.Fatal("reviewer not wired")
	}
	reviewer.reviewer.Observe(core.Result{StopReason: core.StopEndTurn, Messages: []core.Message{core.UserMessage("Write tests", nil)}}, false, true)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("review did not start")
	}
	// Reload directly so this exercises the write-time guard, not cancellation.
	writeTestFile(t, settingsPath, `{"disabledTools":{"ReviewSkill":true}}`)
	if err := m.services.Setting.Reload(m.env.CWD); err != nil {
		t.Fatal(err)
	}
	close(release)
	for range time.Tick(10 * time.Millisecond) {
		if ctx.Err() != nil {
			t.Fatal("review did not finish")
		}
		snapshot := m.services.Session.Snapshot()
		for _, event := range snapshot.Events() {
			if _, ok := event.Payload.(*session.InferenceFinishPayload); ok {
				path := filepath.Join(m.env.CWD, ".san", "skills", "test-workflow", "SKILL.md")
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("disabled reviewer wrote a skill: %v", err)
				}
				return
			}
		}
	}
}

func TestAutoSkillCapabilitiesRejectInvalidSettings(t *testing.T) {
	t.Setenv("SAN_TRUST_WORKSPACE", "1") // project settings apply in a trusted folder
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestFile(t, filepath.Join(cwd, ".san", "settings.json"),
		`{"disabledTools":{"ReviewSkill":false},"autoSkill":{"skills":{"enabled":true,"denyUpdate":true}}}`)

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
		conv:     transcript.NewModel(80),
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
