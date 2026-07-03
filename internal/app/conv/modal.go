package conv

import (
	"github.com/genai-io/san/internal/tool"
)

type ModalState struct {
	Question             *QuestionPrompt
	PendingQuestion      *tool.QuestionRequest
	PendingQuestionReply chan *tool.QuestionResponse
	PendingSecretReply   chan SecretPromptResponse
}

func NewModalState() ModalState {
	return ModalState{
		Question: NewQuestionPrompt(),
	}
}

// QuestionRequestMsg is sent when AskUserQuestion tool is called. Index
// identifies the agent that raised it (0 = the top-level agent).
type QuestionRequestMsg struct {
	Index   int
	Request *tool.QuestionRequest
	Reply   chan *tool.QuestionResponse
}

// QuestionResponseMsg is sent when user answers or cancels
type QuestionResponseMsg struct {
	Request   *tool.QuestionRequest
	Response  *tool.QuestionResponse
	Cancelled bool
}

// SecretPromptRequestMsg is sent when an approved tool needs a secret from the
// user. The reply value is intentionally not represented as a tool question or
// transcript record.
type SecretPromptRequestMsg struct {
	Prompt string
	Reply  chan SecretPromptResponse
}

type SecretPromptResponse struct {
	Value     string
	Cancelled bool
}
