package tool

import "context"

// --- AskUser types ---

// QuestionOption represents a single option for a question.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Question represents a question to ask the user.
type Question struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	Options     []QuestionOption `json:"options"`
	MultiSelect bool             `json:"multiSelect"`
}

// QuestionRequest is sent to the TUI to display questions.
type QuestionRequest struct {
	ID        string
	Questions []Question
}

// QuestionResponse contains the user's answers.
type QuestionResponse struct {
	RequestID string
	Answers   map[int][]string
	Cancelled bool
}

// AskQuestionFunc requests a question response from an interactive caller.
type AskQuestionFunc func(ctx context.Context, req *QuestionRequest) (*QuestionResponse, error)
