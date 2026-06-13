// Imperative user-driven model actions that don't fit a single sub-feature:
// switching the active persona with a hot-patch of the running agent's prompt
// parts and skills.
package app

import (
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/setting"
)

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
