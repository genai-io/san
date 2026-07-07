package conv

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
)

type OutputModel struct {
	Spinner      FrameClock
	MDRenderer   *MDRenderer
	TaskActivity map[int][]string
	AgentToUI    *AgentToUI
	ShowTasks    bool
}

type Model struct {
	ConversationModel
	OutputModel
}

func NewModel(width int) Model {
	agentUI := NewAgentToUI(100)
	return Model{
		ConversationModel: NewConversation(),
		OutputModel: OutputModel{
			Spinner:    newFrameClock(),
			MDRenderer: NewMDRenderer(width),
			AgentToUI:  agentUI,
			ShowTasks:  true,
		},
	}
}

func (m *OutputModel) ResizeMDRenderer(width int) {
	m.MDRenderer = NewMDRenderer(width)
}

// FrameClock wraps the animation spinner together with a monotonic frame
// counter. The spinner's own frame index is unexported and wraps at the frame
// count, so liveness animations can't read it; Frame advances once per real
// frame the spinner consumes, and animations divide it by their own cadence —
// agentBlinkTicks, trackerPulseTicks — to derive their phase.
type FrameClock struct {
	spinner spinner.Model
	frame   int
}

// Update advances the spinner and, on a real frame tick, the frame counter.
// Several code paths schedule Tick, so duplicate tick loops deliver extra
// TickMsgs; the spinner rejects those with a nil cmd (only a frame-advancing
// tick returns one). Counting just the frames it actually consumed keeps the
// clock at the true ~360ms frame rate instead of inflating it.
func (c FrameClock) Update(msg tea.Msg) (FrameClock, tea.Cmd) {
	var cmd tea.Cmd
	c.spinner, cmd = c.spinner.Update(msg)
	if cmd != nil {
		c.frame++
	}
	return c, cmd
}

// View renders the current spinner frame.
func (c FrameClock) View() string { return c.spinner.View() }

// Tick advances the spinner one frame; use the method value (c.Tick) as a tea.Cmd.
func (c FrameClock) Tick() tea.Msg { return c.spinner.Tick() }

// Frame is the monotonic real-frame count that drives liveness animations.
func (c FrameClock) Frame() int { return c.frame }

func newFrameClock() FrameClock {
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{
		Frames: kit.StarSpinnerFrames,
		FPS:    kit.StarSpinnerFPS,
	}
	sp.Style = lipgloss.NewStyle()
	return FrameClock{spinner: sp}
}
