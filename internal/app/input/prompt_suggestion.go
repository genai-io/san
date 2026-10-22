package input

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit/suggest"
)

type PromptSuggestionMsg struct {
	Text string
	Err  error
}

type PromptSuggestionState struct {
	Text   string
	cancel context.CancelFunc
}

func (s *PromptSuggestionState) Clear() {
	s.Text = ""
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// Start runs predict off the UI goroutine, superseding any hint in flight.
func (s *PromptSuggestionState) Start(predict func(context.Context) (string, error)) tea.Cmd {
	s.Clear()
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	return func() tea.Msg {
		text, err := predict(ctx)
		return PromptSuggestionMsg{Text: text, Err: err}
	}
}

func HandlePromptSuggestion(state *Model, active bool, inputValue string, msg PromptSuggestionMsg) {
	if msg.Err != nil {
		return
	}
	if inputValue != "" || active {
		return
	}
	if text := suggest.FilterSuggestion(msg.Text); text != "" {
		state.PromptSuggestion.Text = text
	}
}
