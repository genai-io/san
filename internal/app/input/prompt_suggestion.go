package input

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit/suggest"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
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

const SuggestionSystemPrompt = `You predict what the user will type next in a coding assistant CLI.
Reply with ONLY the predicted text (2-12 words). No quotes, no explanation.
If unsure, reply with nothing.`

const SuggestionUserPrompt = `[PREDICTION MODE] Based on this conversation, predict what the user will type next.
Stay silent if the next step isn't obvious. Match the user's language and style.`

// autopilotSuggestSystemPrompt drives the input hint when the Suggest steer is
// on: it proposes the next step toward the mission as a ready-to-send imperative
// the human can accept, rather than guessing what the human would type.
const autopilotSuggestSystemPrompt = `You are the autopilot copilot for a coding assistant. Given the mission and the session so far, suggest the SINGLE next instruction to give the agent — a short, direct imperative the user can accept and send as-is.
Reply with ONLY that instruction (a few words to one sentence). No quotes, no explanation. Reply with nothing if the mission looks complete or the next step needs a human decision.`

const maxSuggestionMessages = 20

type PromptSuggestionRequest struct {
	Ctx          context.Context
	Client       *llm.Client
	Messages     []core.Message
	SystemPrompt string
	UserPrompt   string
	MaxTokens    int
}

type PromptSuggestionDeps struct {
	Input        *Model
	Conversation *conv.ConversationModel
	HasProvider  bool
	BuildClient  func() *llm.Client
	// Mission, when set, switches the hint from "predict the human's next input"
	// to "propose the next step toward this mission" (the Suggest steer).
	Mission string
	// Silent holds the hint back entirely — set when AutoPilot is engaged with
	// the Suggest steer off, so the copilot doesn't nudge with an input guess.
	Silent bool
}

func StartPromptSuggestion(deps PromptSuggestionDeps) tea.Cmd {
	req, ok := BuildPromptSuggestionRequest(deps)
	if !ok {
		return nil
	}

	deps.Input.PromptSuggestion.Clear()

	ctx, cancel := context.WithCancel(context.Background())
	deps.Input.PromptSuggestion.cancel = cancel
	req.Ctx = ctx

	return SuggestPromptCmd(req)
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

func SuggestPromptCmd(req PromptSuggestionRequest) tea.Cmd {
	if req.Client == nil {
		return nil
	}
	return func() tea.Msg {
		resp, err := req.Client.Complete(req.Ctx, req.SystemPrompt, req.Messages, req.MaxTokens)
		if err != nil {
			return PromptSuggestionMsg{Err: err}
		}
		return PromptSuggestionMsg{Text: resp.Content}
	}
}

func BuildPromptSuggestionRequest(deps PromptSuggestionDeps) (PromptSuggestionRequest, bool) {
	if !deps.HasProvider || deps.Silent {
		return PromptSuggestionRequest{}, false
	}

	startIdx := 0
	if len(deps.Conversation.Messages) > maxSuggestionMessages {
		startIdx = len(deps.Conversation.Messages) - maxSuggestionMessages
	}

	// Suggest steer: propose the next mission step. Works from the very start (no
	// prior-turn requirement), since the opening step comes from the mission.
	if mission := strings.TrimSpace(deps.Mission); mission != "" {
		msgs := deps.Conversation.ConvertToProviderFrom(startIdx)
		msgs = append(msgs, core.Message{Role: core.RoleUser, Content: "Mission:\n" + mission + "\n\nSuggest the next instruction to give the agent."})
		return PromptSuggestionRequest{
			Client:       deps.BuildClient(),
			Messages:     msgs,
			SystemPrompt: autopilotSuggestSystemPrompt,
			MaxTokens:    60,
		}, true
	}

	// Generic prediction needs some conversation to go on.
	assistantCount := 0
	for _, msg := range deps.Conversation.Messages {
		if msg.Role == core.RoleAssistant {
			assistantCount++
		}
	}
	if assistantCount < 2 {
		return PromptSuggestionRequest{}, false
	}
	msgs := deps.Conversation.ConvertToProviderFrom(startIdx)
	msgs = append(msgs, core.Message{Role: core.RoleUser, Content: SuggestionUserPrompt})

	return PromptSuggestionRequest{
		Client:       deps.BuildClient(),
		Messages:     msgs,
		SystemPrompt: SuggestionSystemPrompt,
		UserPrompt:   SuggestionUserPrompt,
		MaxTokens:    60,
	}, true
}
