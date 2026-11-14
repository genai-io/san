package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/tests/integration/testutil"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func extensionReloadModel(t *testing.T, cwd string) *model {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	plugins, agents, skills := plugin.Default(), subagent.Default(), skill.Default()
	commands, personas, servers := command.Default(), persona.Default(), mcp.DefaultManager()
	plugin.SetDefaultRegistry(plugin.NewRegistry())
	subagent.SetDefaultRegistry(subagent.NewRegistry())
	mcp.SetDefaultManager(mcp.NewManagerForTest(nil))
	t.Cleanup(func() {
		_ = mcp.DefaultManager().Close()
		plugin.SetDefaultRegistry(plugins)
		subagent.SetDefaultRegistry(agents)
		skill.SetDefaultRegistry(skills)
		command.SetDefaultRegistry(commands)
		persona.SetDefault(personas)
		mcp.SetDefaultManager(servers)
		setting.Initialize(setting.Options{})
	})
	setting.Initialize(setting.Options{CWD: cwd})
	initExtensions(cwd)
	svc := services{
		Setting: setting.Default(), Skill: skill.Default(), Command: command.Default(),
		Subagent: subagent.Default(), MCP: mcp.DefaultManager(), Plugin: plugin.Default(), Persona: persona.Default(),
		Agent: &agent.Runtime{}, Tool: tool.NewRegistry(), Reminder: reminder.New(),
		Hook: hook.NewEngine(setting.NewData(), "", cwd, ""),
	}
	m := &model{env: env{CWD: cwd}, services: svc, learnedStores: newLearnedStoreContext(cwd, svc.Setting)}
	m.userInput = input.New(cwd, 80, commandSuggestionMatcher(svc.Command, svc.Tool), input.SelectorDeps{
		AgentRegistry: svc.Subagent, PersonaRegistry: svc.Persona, SkillRegistry: svc.Skill,
		MCPRegistry: svc.MCP, PluginRegistry: svc.Plugin, Setting: svc.Setting,
		LoadDisabled: svc.Setting.DisabledToolsAt, UpdateDisabled: svc.Setting.UpdateDisabledToolsAt,
	})
	return m
}

func writeReloadFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionReloadPluginContributions(t *testing.T) {
	m := extensionReloadModel(t, t.TempDir())
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "probe")
	commandPath := filepath.Join(root, "commands", "probe.md")
	writeReloadFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: probe\ndescription: Reload probe\n---\nProbe instructions\n")
	writeReloadFile(t, commandPath, "---\ndescription: Reload command\n---\nProbe prompt\n")
	m.services.Plugin.Register(&plugin.Plugin{
		Manifest: plugin.Manifest{Name: "reload"}, Path: root, Enabled: true, Scope: plugin.ScopeProject,
		Components: plugin.Components{Skills: []string{skillDir}, Commands: []string{commandPath},
			Hooks: &plugin.HooksConfig{Hooks: map[string][]plugin.HookMatcher{"PreToolUse": {{Matcher: "Bash"}}}}},
	})
	for _, enabled := range []bool{true, false} {
		if !enabled {
			m.services.Plugin.Unregister("reload")
		}
		if err := m.ReloadAfterPluginChange(); err != nil {
			t.Fatal(err)
		}
		if err := m.userInput.Skill.Selector.EnterSelect(80, 24); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(m.userInput.Skill.Selector.Render(), "probe"); got != enabled {
			t.Errorf("plugin enabled=%v, skill selector contains probe=%v", enabled, got)
		}
		_, found := m.services.Command.Get("reload:probe")
		if found != enabled {
			t.Errorf("plugin enabled=%v, command present=%v", enabled, found)
		}
		if got := m.services.Hook.HasHooks(hook.PreToolUse); got != enabled {
			t.Errorf("plugin enabled=%v, hook present=%v", enabled, got)
		}
	}
}

func TestExtensionReloadProjectSuggestions(t *testing.T) {
	old, next := t.TempDir(), t.TempDir()
	writeReloadFile(t, filepath.Join(old, ".san", "commands", "old-project.md"), "Old project prompt\n")
	writeReloadFile(t, filepath.Join(next, ".san", "commands", "new-project.md"), "New project prompt\n")
	for cwd, name := range map[string]string{old: "old-helper", next: "new-helper"} {
		writeReloadFile(t, filepath.Join(cwd, ".san", "settings.json"), `{"persona":"go"}`)
		writeReloadFile(t, filepath.Join(cwd, ".san", "personas", "go", "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: Persona probe\n---\nPersona instructions\n")
	}
	m := extensionReloadModel(t, old)
	m.applyPersonaSkills()
	m.userInput.Suggestions.UpdateSuggestions("/old-project")
	if m.userInput.Suggestions.Selected() != "/old-project" {
		t.Fatal("old-project fixture was not loaded")
	}
	before := m.services.Setting
	m.env.CWD = next
	m.ReloadProjectContext(next)
	if m.services.Setting == before {
		t.Fatal("settings handle was not replaced")
	}
	m.userInput.Suggestions.UpdateSuggestions("/new-project")
	if got := m.userInput.Suggestions.Selected(); got != "/new-project" {
		t.Errorf("new-project suggestion = %q", got)
	}
	m.userInput.Suggestions.UpdateSuggestions("/old-project")
	if got := m.userInput.Suggestions.Selected(); got != "" {
		t.Errorf("old project suggestion survived reload: %q", got)
	}
	cwd, settings := m.learnedStores.Snapshot()
	if cwd != next || settings != m.services.Setting {
		t.Fatal("learned stores retained the old workspace")
	}
	if skills := m.services.Skill.List(); !slices.ContainsFunc(skills, func(s *skill.Skill) bool { return s.Name == "new-helper" }) || slices.ContainsFunc(skills, func(s *skill.Skill) bool { return s.Name == "old-helper" }) {
		t.Fatal("persona skills were not reapplied for the new workspace")
	}
}

func TestExtensionReloadReconcilesRunningMCPTools(t *testing.T) {
	for _, path := range []string{"plugin", "project"} {
		t.Run(path, func(t *testing.T) {
			m := extensionReloadModel(t, t.TempDir())
			if err := m.services.Agent.Start(agent.BuildParams{Provider: &testutil.FakeProvider{}, ModelID: "m"}, nil); err != nil {
				t.Fatal(err)
			}
			defer stopRuntime(t, m.services.Agent)
			tools := m.services.Agent.Tools()
			tools.Add(namedTool("mcp__removed__read"), "test")
			tools.Add(namedTool("Read"), "test")
			if path == "plugin" {
				if err := m.ReloadAfterPluginChange(); err != nil {
					t.Fatal(err)
				}
			} else {
				m.ReloadProjectContext(m.env.CWD)
			}
			var names []string
			for _, schema := range tools.Schemas() {
				names = append(names, schema.Name)
			}
			if slices.Contains(names, "mcp__removed__read") || !slices.Contains(names, "Read") {
				t.Fatalf("reload left the wrong live toolset: %v", names)
			}
		})
	}
}

func TestExtensionReloadRebuildsTheNextRuntime(t *testing.T) {
	m, _ := resumedModel(t, nil)
	extensions := extensionReloadModel(t, m.env.CWD)
	m.services.Skill = extensions.services.Skill
	m.services.Command = extensions.services.Command
	m.services.Subagent = extensions.services.Subagent
	m.services.MCP = extensions.services.MCP
	m.services.Plugin = extensions.services.Plugin
	m.services.Persona = extensions.services.Persona
	m.services.Tool = tool.Default()
	m.learnedStores = extensions.learnedStores
	m.userInput = extensions.userInput
	m.env.LLMProvider = acpTestProvider{aitest.Always(aitest.Says("unused")).Client()}
	if _, err := m.ensureAgentRuntime(); err != nil {
		t.Fatal(err)
	}
	owner, tools := m.services.Session, m.services.Agent.Tools()
	if err := m.ReloadAfterPluginChange(); err != nil {
		t.Fatal(err)
	}
	if !m.services.Agent.Active() || m.services.Agent.Tools() != tools {
		t.Fatal("reload interrupted the current runtime")
	}
	if _, err := m.ensureAgentRuntime(); err != nil {
		t.Fatal(err)
	}
	if m.services.Agent.Tools() == tools || m.agentReloadPending {
		t.Fatal("next turn reused captured extension and permission dependencies")
	}
	if m.services.Session != owner {
		t.Fatal("extension reload replaced the recording owner")
	}
}
