package conv

import (
	"github.com/genai-io/san/v2/internal/tool"
)

type ModalState struct {
	Question             *QuestionPrompt
	PendingQuestion      *tool.QuestionRequest
	PendingQuestionReply chan *tool.QuestionResponse
}

func NewModalState() ModalState {
	return ModalState{
		Question: NewQuestionPrompt(),
	}
}

// QuestionRequestMsg is sent when AskUserQuestion tool is called
type QuestionRequestMsg struct {
	Request *tool.QuestionRequest
	Reply   chan *tool.QuestionResponse
}

// QuestionResponseMsg is sent when user answers or cancels
type QuestionResponseMsg struct {
	Request   *tool.QuestionRequest
	Response  *tool.QuestionResponse
	Cancelled bool
}
