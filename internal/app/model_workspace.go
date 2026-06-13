// Reactions to workspace changes: cwd switch (Bash `cd`, EnterWorktree,
// ExitWorktree), file-change notifications fed to hooks, project-context
// reload when cwd changes, persona reload when the user edits a persona or
// identity file, and FileWatcher setup off the SessionStart hook outcome.
package app

import (
	"github.com/genai-io/san/internal/app/trigger"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/setting"
)

func (m *model) changeCwd(newCwd string) {
	if newCwd == "" || newCwd == m.env.CWD {
		return
	}
	oldCwd := m.env.CWD
	m.env.CWD = newCwd
	m.env.IsGit = m.services.Setting.IsGitRepo(newCwd)
	m.userInput.HandleCwdChange(newCwd)
	m.env.ClearCachedInstructions()
	m.refreshMemoryContext(newCwd, "cwd_changed")
	m.ReloadProjectContext(newCwd)
	m.ReconfigureAgentTool()
	if m.services.Hook != nil {
		m.services.Hook.SetCwd(newCwd)
		m.services.Hook.ExecuteAsync(hook.CwdChanged, hook.HookInput{OldCwd: oldCwd, NewCwd: newCwd})
	}
}

func (m *model) fireFileChanged(filePath, source string) {
	if m.services.Hook == nil || filePath == "" {
		return
	}
	m.services.Hook.ExecuteAsync(hook.FileChanged, hook.HookInput{FilePath: filePath, Source: source, Event: "change"})
}

func (m *model) ReloadProjectContext(cwd string) {
	initExtensions(cwd)
	setting.Initialize(setting.Options{CWD: cwd})
	m.services.refreshAfterReload()
	if m.services.Hook != nil {
		plugin.MergePluginHooksIntoSettings(m.services.Setting.Snapshot())
	}
	m.syncSettingsToHookEngine()
}

func (m *model) reloadPersonasIfChanged(filePath string) {
	if m.services.Persona == nil || !persona.IsPersonaFile(m.env.CWD, filePath) {
		return
	}
	m.services.Persona.Reload()
	m.applyPersonaSkills()
	m.ReconfigureAgentTool()
}

func (m *model) applyStartupHookOutcome(outcome hook.HookOutcome) {
	if outcome.InitialUserMessage != "" && m.env.InitialPrompt == "" && len(m.conv.Messages) == 0 {
		m.env.InitialPrompt = outcome.InitialUserMessage
	}
	if len(outcome.WatchPaths) == 0 {
		return
	}
	if m.systemInput.FileWatcher == nil {
		m.systemInput.FileWatcher = trigger.NewFileWatcher(m.services.Hook, func(outcome hook.HookOutcome) {
			if m.systemInput.AsyncHookQueue != nil && outcome.InitialUserMessage != "" {
				m.systemInput.AsyncHookQueue.Push(trigger.AsyncHookRewake{Notice: "File watcher hook triggered", Context: []string{outcome.InitialUserMessage}})
			}
		})
	}
	m.systemInput.FileWatcher.SetPaths(outcome.WatchPaths)
}
