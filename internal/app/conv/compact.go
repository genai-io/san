// Compact presentation state and commands.
package conv

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/kit"
)

// CompactResultMsg is sent when a compaction operation completes.
type CompactResultMsg = agent.CompactResult

type CompactRequest = agent.CompactRequest

const PhaseSummarizing = "Summarizing conversation history"

type CompactState struct {
	Active            bool
	SummaryFocus      string
	LastResult        string
	LastError         bool
	Phase             string
	Count             int // messages being compacted, shown in the in-progress line
	WarningSuppressed bool
}

func (c *CompactState) ClearResult() {
	c.LastResult = ""
	c.LastError = false
}

// Clear stops progress without touching the completed-summary result fields.
func (c *CompactState) Clear() {
	c.Active = false
	c.Phase = ""
}

func (c *CompactState) Complete(result string, isError bool) {
	c.Active = false
	c.SummaryFocus = ""
	c.LastResult = result
	c.LastError = isError
	c.Phase = ""
	if !isError {
		c.WarningSuppressed = true
	}
}

// RenderCompactStatus shows progress or failure; success has a boundary line.
func RenderCompactStatus(_ int, spinnerView string, state CompactState) string {
	switch {
	case state.Active:
		msg := "Compacting conversation…"
		if state.Count > 0 {
			msg = fmt.Sprintf("Compacting %d messages…", state.Count)
		}
		return lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.TextDim).
			PaddingLeft(1).
			Render(spinnerView + " " + msg)
	case state.LastResult != "" && state.LastError:
		return lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Error).
			PaddingLeft(1).
			Render("✗ " + state.LastResult)
	default:
		return ""
	}
}

func CompactCmd(req CompactRequest) tea.Cmd {
	return func() tea.Msg { return req.Run() }
}
