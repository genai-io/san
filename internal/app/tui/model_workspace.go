// Reactions to workspace changes: cwd switch (Bash `cd`), file-change
// notifications fed to hooks, project-context
// reload when cwd changes, persona reload when the user edits a persona file,
// and FileWatcher setup off the SessionStart hook outcome.
package tui

import (
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/infra/proc"
	"github.com/genai-io/san/internal/persona"
)

func (m *model) changeCwd(newCwd string) {
	if newCwd == "" || newCwd == m.env.CWD {
		return
	}
	oldCwd := m.env.CWD
	m.env.CWD = newCwd
	m.env.IsGit = m.services.Setting.IsGitRepo(newCwd)
	// Publish only after both facts are resolved. The running agent's tools
	// and reviewer must see the new workspace without waiting for a rebuild.
	m.refreshAutopilotSnapshot()
	if m.services.Agent != nil {
		if sys := m.services.Agent.System(); sys != nil {
			sys.Use(system.EnvironmentSection(system.Environment{Cwd: newCwd, Shell: proc.DefaultShellName()}), "cwd_changed")
		}
	}
	m.userInput.HandleCwdChange(newCwd)
	m.env.ClearCachedInstructions()
	m.refreshMemoryContext(newCwd, "cwd_changed")
	m.ReloadProjectContext(newCwd)
	m.ReconfigureAgentTool()
	m.services.Hook.SetCwd(newCwd)
	m.services.Hook.ExecuteAsync(hook.CwdChanged, hook.HookInput{OldCwd: oldCwd, NewCwd: newCwd})
}

func (m *model) fireFileChanged(filePath, source string) {
	if filePath == "" {
		return
	}
	m.services.Hook.ExecuteAsync(hook.FileChanged, hook.HookInput{FilePath: filePath, Source: source, Event: "change"})
}

func (m *model) ReloadProjectContext(cwd string) {
	// cwd changed: discover the new project's plugins, then rebuild the project
	// feature services that depend on them and re-point at them.
	m.noteUntrustedWorkspace(cwd)
	discoverPlugins(cwd)
	m.reloadProjectServices(cwd)
	m.env.ShowGitBar = m.services.Setting.Snapshot().ShowGitBar()
	m.syncSettingsToHookEngine()
	m.applyPersonaSkills()
	m.applyPersonaAgents()
	if m.services.Reminder != nil {
		m.services.Reminder.RequeueSystemReminders()
	}
}

func (m *model) reloadPersonasIfChanged(filePath string) {
	if !persona.IsPersonaFile(m.env.CWD, filePath) {
		return
	}
	m.services.Persona.Reload()
	m.applyPersonaSkills()
	m.applyPersonaAgents()
	m.ReconfigureAgentTool()
}

func (m *model) applyStartupHookOutcome(outcome hook.HookOutcome) {
	if outcome.InitialUserMessage != "" && m.env.InitialPrompt == "" && len(m.conv.Messages) == 0 {
		m.env.InitialPrompt = outcome.InitialUserMessage
	}
	if len(outcome.WatchPaths) == 0 {
		return
	}
	if m.fileWatcher == nil {
		m.fileWatcher = newFileWatcher(m.services.Hook, func(outcome hook.HookOutcome) {
			if m.asyncHookQueue != nil && outcome.InitialUserMessage != "" {
				m.asyncHookQueue.push(asyncHookRewake{Notice: "File watcher hook triggered", Context: []string{outcome.InitialUserMessage}})
			}
		})
	}
	m.fileWatcher.setPaths(outcome.WatchPaths)
}
