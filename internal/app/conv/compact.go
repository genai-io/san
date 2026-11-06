// Compact presentation state and commands.
package conv

import (
	"context"
	"time"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/kit"
)

// CompactResultMsg carries the caller context as the exact request identity.
// A cancelled request may still complete; the app must fence it before effects.
type CompactResultMsg struct {
	agent.CompactResult
	Context context.Context
}

const PhaseSummarizing = "Summarizing conversation history"

type CompactState struct {
	Active            bool
	SummaryFocus      string
	LastResult        string
	LastError         bool
	Phase             string
	Count             int // messages being compacted
	AutoContinue      bool
	StartedAt         time.Time
	WarningSuppressed bool
}

func (c *CompactState) ClearResult() {
	c.LastResult = ""
	c.LastError = false
}

// Clear stops progress without touching the completed-summary result fields.
func (c *CompactState) Clear() {
	c.Active = false
	c.AutoContinue = false
	c.StartedAt = time.Time{}
	c.Phase = ""
}

func (c *CompactState) Complete(result string, isError bool) {
	c.Active = false
	c.AutoContinue = false
	c.StartedAt = time.Time{}
	c.SummaryFocus = ""
	c.LastResult = result
	c.LastError = isError
	c.Phase = ""
	if !isError {
		c.WarningSuppressed = true
	}
}

// RenderCompactStatus shows progress or failure; success has a boundary line.
func RenderCompactStatus(width int, spinnerView string, state CompactState) string {
	switch {
	case state.Active:
		msg := "Compacting context…"
		if state.AutoContinue {
			msg = "Compacting context · will continue automatically"
		}
		if !state.StartedAt.IsZero() {
			msg += " · " + formatElapsedTime(state.StartedAt)
		}
		row := lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.TextDim).
			PaddingLeft(1).
			Render(spinnerView + " " + msg)
		return ansi.Truncate(row, max(1, width), "…")
	case state.LastResult != "" && state.LastError:
		return lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Error).
			PaddingLeft(1).
			Render("✗ " + state.LastResult)
	default:
		return ""
	}
}

func CompactCmd(req agent.CompactRequest) tea.Cmd {
	return func() tea.Msg { return CompactResultMsg{CompactResult: req.Run(), Context: req.Ctx} }
}
