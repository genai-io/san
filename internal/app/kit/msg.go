package kit

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// DismissedMsg is sent when any selector or overlay is dismissed without selection.
type DismissedMsg struct{}

// StatusExpiredMsg signals that a temporary status message should be cleared.
type StatusExpiredMsg struct {
	Token int64
}

// StatusTimer returns a tea.Cmd that clears the status message after the given duration.
func StatusTimer(d time.Duration, token int64) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return StatusExpiredMsg{Token: token}
	})
}
