package conv

import (
	"github.com/genai-io/san/internal/conversation"
	"github.com/genai-io/san/internal/tool"
)

type ModalState struct {
	Question *QuestionPrompt
}

func NewModalState() ModalState {
	return ModalState{
		Question: NewQuestionPrompt(),
	}
}

// QuestionRequestMsg is the question payload and its original reply endpoint.
// Worker attribution and lifetime belong to the enclosing InteractionRequest.
type QuestionRequestMsg = conversation.Question

// QuestionResponseMsg is sent when user answers or cancels
type QuestionResponseMsg struct {
	Request   *tool.QuestionRequest
	Response  *tool.QuestionResponse
	Cancelled bool
}

// SecretPromptRequestMsg is sent when an approved tool needs a secret from the
// user. The reply value is intentionally not represented as a tool question or
// transcript record.
type SecretPromptRequestMsg = conversation.Secret

type SecretPromptResponse = conversation.SecretResponse
