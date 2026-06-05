package input

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/identity"
)

// UpdateIdentity routes identity-selector messages to the configured
// callbacks. Returns (cmd, true) if the message belongs to this overlay.
func UpdateIdentity(deps OverlayDeps, msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case IdentityActivateMsg:
		return handleIdentityActivate(deps, m), true
	case IdentitySlashMsg:
		return handleIdentitySlash(deps, m), true
	}
	return nil, false
}

func handleIdentityActivate(deps OverlayDeps, msg IdentityActivateMsg) tea.Cmd {
	if deps.SetActiveIdentity == nil {
		return nil
	}
	notice := "Identity set to: " + identityLabel(msg.Name)
	if err := deps.SetActiveIdentity(msg.Name); err != nil {
		notice = "Failed to activate identity: " + err.Error()
	}
	deps.Conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: notice})
	return tea.Batch(deps.CommitMessages()...)
}

func identityLabel(name string) string {
	if name == "" {
		return identity.DefaultName
	}
	return name
}

func handleIdentitySlash(deps OverlayDeps, msg IdentitySlashMsg) tea.Cmd {
	if deps.DispatchSlashCommand == nil {
		return nil
	}
	return deps.DispatchSlashCommand(msg.Command)
}
