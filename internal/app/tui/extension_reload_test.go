package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/plugin"
	agentruntime "github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/runtime/reminder"
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
	commands, personas, servers := command.Default(), persona.Default(), mcp.Default()
	plugin.SetDefaultRegistry(plugin.NewRegistry())
	subagent.SetDefaultRegistry(subagent.NewRegistry())
	mcp.SetDefault(mcp.NewServerSetForTest(nil))
	t.Cleanup(func() {
		_ = mcp.Default().Close()
		plugin.SetDefaultRegistry(plugins)
		subagent.SetDefaultRegistry(agents)
		skill.SetDefaultRegistry(skills)
		command.SetDefaultRegistry(commands)
		persona.SetDefault(personas)
		mcp.SetDefault(servers)
		setting.Initialize(setting.Options{})
	})
	setting.Initialize(setting.Options{CWD: cwd})
	initExtensions(cwd)
	features := appFeatures{
		Setting: setting.Default(), Skill: skill.Default(), Command: command.Default(),
		Subagent: subagent.Default(), MCP: mcp.Default(), Plugin: plugin.Default(), Persona: persona.Default(),
		Agent: &agentruntime.Runtime{}, Tool: tool.NewRegistry(), Reminder: reminder.NewQueue(),
		Hook: hook.NewEngine(setting.NewData(), "", cwd, ""),
	}
	m := &model{env: env{CWD: cwd}, features: features, learnedStores: newLearnedStoreContext(cwd, features.Setting)}
	m.userInput = input.New(cwd, 80, commandSuggestionMatcher(features.Command, features.Flow), input.SelectorBindings{
		AgentRegistry: features.Subagent, PersonaRegistry: features.Persona, SkillRegistry: features.Skill,
		MCPServers: features.MCP, PluginRegistry: features.Plugin, Setting: features.Setting,
		LoadDisabled: features.Setting.DisabledToolsAt, UpdateDisabled: features.Setting.UpdateDisabledToolAt,
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
	m.features.Plugin.Register(&plugin.Plugin{
		Manifest: plugin.Manifest{Name: "reload"}, Path: root, Enabled: true, Scope: plugin.ScopeProject,
		Components: plugin.Components{Skills: []string{skillDir}, Commands: []string{commandPath},
			Hooks: &plugin.HooksConfig{Hooks: map[string][]plugin.HookMatcher{"PreToolUse": {{Matcher: "Bash"}}}}},
	})
	for _, enabled := range []bool{true, false} {
		if !enabled {
			m.features.Plugin.Unregister("reload")
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
		_, found := m.features.Command.Get("reload:probe")
		if found != enabled {
			t.Errorf("plugin enabled=%v, command present=%v", enabled, found)
		}
		if got := m.features.Hook.HasHooks(hook.PreToolUse); got != enabled {
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
	before := m.features.Setting
	m.env.CWD = next
	m.ReloadProjectContext(next)
	if m.features.Setting == before {
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
	if cwd != next || settings != m.features.Setting {
		t.Fatal("learned stores retained the old workspace")
	}
	if skills := m.features.Skill.List(); !slices.ContainsFunc(skills, func(s *skill.Skill) bool { return s.Name == "new-helper" }) || slices.ContainsFunc(skills, func(s *skill.Skill) bool { return s.Name == "old-helper" }) {
		t.Fatal("persona skills were not reapplied for the new workspace")
	}
}

func TestExtensionReloadReconcilesRunningMCPTools(t *testing.T) {
	for _, path := range []string{"plugin", "project"} {
		t.Run(path, func(t *testing.T) {
			m := extensionReloadModel(t, t.TempDir())
			if err := m.features.Agent.Start(agentruntime.BuildParams{Provider: &testutil.FakeProvider{}, ModelID: "m"}, nil); err != nil {
				t.Fatal(err)
			}
			defer stopRuntime(t, m.features.Agent)
			tools := m.features.Agent.Tools()
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
	m.features.Skill = extensions.features.Skill
	m.features.Command = extensions.features.Command
	m.features.Subagent = extensions.features.Subagent
	m.features.MCP = extensions.features.MCP
	m.features.Plugin = extensions.features.Plugin
	m.features.Persona = extensions.features.Persona
	m.features.Tool = tool.Default()
	m.learnedStores = extensions.learnedStores
	m.userInput = extensions.userInput
	m.env.LLMProvider = staticTestProvider{aitest.Always(aitest.Says("unused")).Client()}
	if _, err := m.ensureAgentRuntime(); err != nil {
		t.Fatal(err)
	}
	owner, tools := m.features.Session, m.features.Agent.Tools()
	if err := m.ReloadAfterPluginChange(); err != nil {
		t.Fatal(err)
	}
	if !m.features.Agent.Active() || m.features.Agent.Tools() != tools {
		t.Fatal("reload interrupted the current runtime")
	}
	if _, err := m.ensureAgentRuntime(); err != nil {
		t.Fatal(err)
	}
	if m.features.Agent.Tools() == tools || m.agentReloadPending {
		t.Fatal("next turn reused captured extension and permission dependencies")
	}
	if m.features.Session != owner {
		t.Fatal("extension reload replaced the recording owner")
	}
}
