// Imperative user-driven model actions that don't fit a single sub-feature:
// switching the active identity (with hot-patch of the running agent's
// system prompt), and dispatching an arbitrary slash command from a
// selector hotkey as if the user had typed it.
package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/v2/internal/app/input"
	"github.com/genai-io/san/v2/internal/core/system"
	"github.com/genai-io/san/v2/internal/setting"
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

// dispatchSlashCommand runs an arbitrary slash command as if the user had
// typed it. Used by selector hotkeys (Shift+N / Shift+E in /identity).
func (m *model) dispatchSlashCommand(cmd string) tea.Cmd {
	ctrl := input.NewSlashCommandController(m.slashCommandEnv())
	teaCmd, _ := ctrl.HandleSubmit(cmd)
	return teaCmd
}
