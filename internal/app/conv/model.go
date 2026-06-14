package conv

import (
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
)

type OutputModel struct {
	Spinner      spinner.Model
	Blink        int
	MDRenderer   *MDRenderer
	TaskProgress map[int][]string
	ProgressHub  *ProgressHub
	ShowTasks    bool
}

type Model struct {
	ConversationModel
	OutputModel
}

func NewModel(width int) Model {
	hub := NewProgressHub(100)
	return Model{
		ConversationModel: NewConversation(),
		OutputModel: OutputModel{
			Spinner:     newSpinner(),
			MDRenderer:  NewMDRenderer(width),
			ProgressHub: hub,
			ShowTasks:   true,
		},
	}
}

func (m *OutputModel) ResizeMDRenderer(width int) {
	m.MDRenderer = NewMDRenderer(width)
}

func newSpinner() spinner.Model {
	sp := spinner.New()
	// 4-pt → 6-pt → 8-pt → 6-pt stars read as a rotating sparkle while
	// the model is thinking / streaming.
	sp.Spinner = spinner.Spinner{
		Frames: []string{"✦", "✶", "✸", "✶"},
		FPS:    360 * time.Millisecond,
	}
	sp.Style = lipgloss.NewStyle()
	return sp
}
