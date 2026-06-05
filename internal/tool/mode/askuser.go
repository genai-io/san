package mode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// AskUserQuestionTool prompts the user for input
type AskUserQuestionTool struct {
	requestCounter int
}

const (
	maxAskUserQuestions = 8
	maxAskUserOptions   = 8
)

// NewAskUserQuestionTool creates a new AskUserQuestionTool
func NewAskUserQuestionTool() *AskUserQuestionTool {
	return &AskUserQuestionTool{}
}

func (t *AskUserQuestionTool) Name() string {
	return "AskUserQuestion"
}

func (t *AskUserQuestionTool) Description() string {
	return "Ask the user questions to gather preferences, clarify requirements, or get decisions on implementation choices."
}

func (t *AskUserQuestionTool) Icon() string {
	return "❓"
}

func (t *AskUserQuestionTool) RequiresInteraction() bool {
	return true
}

// inputQuestion is the simplified per-question structure from the LLM.
type inputQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

// parseInput normalizes any format the LLM might send into []inputQuestion.
// Supported formats:
//
//	{"question": "...", "options": ["a","b"]}
//	{"options": ["a","b"]}                       (question defaults to "Please choose:")
//	{"questions": [{"question":"...", "options":["a","b"]}, ...]}
func parseInput(params map[string]any) ([]inputQuestion, error) {
	if questionsRaw, ok := params["questions"]; ok {
		data, err := json.Marshal(questionsRaw)
		if err != nil {
			return nil, fmt.Errorf("invalid questions format: %w", err)
		}
		var input []inputQuestion
		if err := json.Unmarshal(data, &input); err != nil {
			return nil, fmt.Errorf("questions must be an array of {question, options}: %w", err)
		}
		return input, nil
	}

	optsRaw, hasOpts := params["options"]
	if !hasOpts {
		return nil, fmt.Errorf("missing required parameter: options (or questions)")
	}
	data, err := json.Marshal(optsRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid options format: %w", err)
	}
	var opts []string
	if err := json.Unmarshal(data, &opts); err != nil {
		return nil, fmt.Errorf("options must be an array of strings: %w", err)
	}
	q, _ := params["question"].(string)
	if q == "" {
		q = "Please choose:"
	}
	return []inputQuestion{{Question: q, Options: opts}}, nil
}

// PrepareInteraction parses questions and returns a QuestionRequest
func (t *AskUserQuestionTool) PrepareInteraction(ctx context.Context, params map[string]any, cwd string) (any, error) {
	input, err := parseInput(params)
	if err != nil {
		return nil, err
	}

	if len(input) == 0 || len(input) > maxAskUserQuestions {
		return nil, fmt.Errorf("must have 1-%d questions, got %d", maxAskUserQuestions, len(input))
	}

	questions := make([]tool.Question, len(input))
	for i, q := range input {
		if q.Question == "" {
			return nil, fmt.Errorf("question[%d]: question text is required", i)
		}
		if len(q.Options) < 2 || len(q.Options) > maxAskUserOptions {
			return nil, fmt.Errorf("question[%d]: must have 2-%d options, got %d", i, maxAskUserOptions, len(q.Options))
		}
		opts := make([]tool.QuestionOption, len(q.Options))
		for j, label := range q.Options {
			if label == "" {
				return nil, fmt.Errorf("question[%d].options[%d]: label must not be empty", i, j)
			}
			opts[j] = tool.QuestionOption{Label: label}
		}
		header := fmt.Sprintf("Q%d", i+1)
		if len(input) == 1 {
			header = "Choose"
		}
		questions[i] = tool.Question{
			Question: q.Question,
			Header:   header,
			Options:  opts,
		}
	}

	t.requestCounter++
	return &tool.QuestionRequest{
		ID:        fmt.Sprintf("ask-%d", t.requestCounter),
		Questions: questions,
	}, nil
}

// ExecuteWithResponse formats the user's response for the LLM
func (t *AskUserQuestionTool) ExecuteWithResponse(ctx context.Context, params map[string]any, response any, cwd string) toolresult.ToolResult {
	resp, ok := response.(*tool.QuestionResponse)
	if !ok {
		return toolresult.NewErrorResult("AskUserQuestion", "invalid response type")
	}

	if resp.Cancelled {
		return toolresult.ToolResult{
			Success: true,
			Output:  "User cancelled the question prompt without answering.",
			Metadata: toolresult.ResultMetadata{
				Title:    "AskUserQuestion",
				Icon:     "❓",
				Subtitle: "Cancelled",
			},
		}
	}

	input, _ := parseInput(params)

	var parts []string
	for i, answers := range resp.Answers {
		if i >= len(input) {
			break
		}
		sel := strings.Join(answers, ", ")
		if sel == "" {
			sel = "(no selection)"
		}
		parts = append(parts, fmt.Sprintf("%s → %s", input[i].Question, sel))
	}

	if len(parts) == 0 {
		return toolresult.ToolResult{
			Success:  true,
			Output:   "User did not select any option.",
			Metadata: toolresult.ResultMetadata{Title: "AskUserQuestion", Icon: "❓", Subtitle: "No selection"},
		}
	}

	output := "User responses:\n" + strings.Join(parts, "\n")
	subtitle := strings.Join(parts, "; ")
	if len(subtitle) > 60 {
		subtitle = subtitle[:57] + "..."
	}
	return toolresult.ToolResult{
		Success: true,
		Output:  output,
		Metadata: toolresult.ResultMetadata{
			Title:    "AskUserQuestion",
			Icon:     "❓",
			Subtitle: subtitle,
		},
	}
}

// Execute should not be called directly for interactive tools
func (t *AskUserQuestionTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return toolresult.NewErrorResult("AskUserQuestion", "this tool requires user interaction - use PrepareInteraction and ExecuteWithResponse")
}

func init() {
	tool.Register(NewAskUserQuestionTool())
}
