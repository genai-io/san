// Imperative user-driven model actions that don't fit a single sub-feature:
// switching the active identity (with hot-patch of the running agent's
// system prompt), and dispatching an arbitrary slash command from a
// selector hotkey as if the user had typed it.
package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/setting"
)

// setActiveIdentity persists the user's identity choice and applies it
// without restarting the session: the running main agent's identity slot
// is hot-patched in place (visible on its next inference), and the
// subagent executor is rebuilt so future Agent tool calls inherit the new
// persona. Empty name = revert to built-in default.
func (m *model) setActiveIdentity(name string) error {
	if m.services.Setting != nil {
		if snap := m.services.Setting.Snapshot(); snap != nil && snap.Identity == name {
			return nil
		}
	}
	if err := setting.SaveIdentity(name); err != nil {
		return err
	}
	if m.services.Setting != nil {
		_ = m.services.Setting.Reload(m.env.CWD)
	}
	if m.services.Agent != nil {
		if sys := m.services.Agent.System(); sys != nil {
			system.SwapIdentity(sys, m.activeIdentityBody())
		}
	}
	m.ReconfigureAgentTool()
	return nil
}

// setActivePersona persists the persona choice and applies it without
// restarting the session. Immediate: the persona's skills swap in-memory and
// the running main agent's prompt parts (identity / behavior / rules) are
// hot-patched, both visible on the next inference. The persona's settings
// overlay (disabled tools, permissions) takes effect on the next agent
// rebuild. Empty name = no persona (built-in defaults).
func (m *model) setActivePersona(name string) error {
	if m.services.Setting != nil {
		if snap := m.services.Setting.Snapshot(); snap != nil && snap.Persona == name {
			return nil
		}
	}
	// Save the selection at the scope where the persona lives: a project
	// persona persists in .san/settings.json (the choice stays with the project
	// and doesn't leak to others); user/builtin personas persist user-level.
	userLevel := true
	if m.services.Persona != nil {
		if p, ok := m.services.Persona.Get(name); ok && p.Scope == persona.ScopeProject {
			userLevel = false
		}
	}
	if err := setting.SavePersonaAt(m.env.CWD, name, userLevel); err != nil {
		return err
	}
	if m.services.Setting != nil {
		_ = m.services.Setting.Reload(m.env.CWD)
	}

	// Skills (immediate): swap the in-memory persona skill set, then re-emit
	// the skills-directory reminder so the model sees it on the next turn.
	m.applyPersonaSkills()
	if m.services.Reminder != nil {
		m.services.Reminder.RequeueSystemReminders()
	}

	// Prompt (immediate): hot-patch the running main agent's parts.
	if m.services.Agent != nil {
		if sys := m.services.Agent.System(); sys != nil {
			provider := ""
			if m.env.LLMProvider != nil {
				provider = m.env.LLMProvider.Name()
			}
			system.SwapPersona(sys, m.personaPrompt(), m.env.IsGit, provider)
		}
	}
	m.ReconfigureAgentTool()
	return nil
}

// dispatchSlashCommand runs an arbitrary slash command as if the user had
// typed it. Used by selector hotkeys (Shift+N / Shift+E in /identity).
func (m *model) dispatchSlashCommand(cmd string) tea.Cmd {
	ctrl := input.NewSlashCommandController(m.slashCommandEnv())
	teaCmd, _ := ctrl.HandleSubmit(cmd)
	return teaCmd
}
