package automemory

import "github.com/genai-io/sdk-go/pkg/ai"

const toolForgetMemory = "ForgetMemory"

// This schema is local to Auto Memory Update, not the main tool registry.
func forgetMemorySchema() ai.Schema {
	return ai.Schema{
		Name:        toolForgetMemory,
		Description: "Remove one note already read in this review from the current project's Auto Memory. Use only when the user clearly asks to forget it. San updates the index.",
		Definition: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file": map[string]any{"type": "string", "description": "Indexed note filename, without a directory or path."},
			},
			"required":             []string{"file"},
			"additionalProperties": false,
		},
	}
}

func forgetToolProposal(input string) (proposal, error) {
	var args struct {
		File string `json:"file"`
	}
	if !decodeUpdateInput(input, &args) || !validName(args.File) {
		return proposal{}, errInvalidProposal
	}
	return proposal{Action: actionForget, File: args.File}, nil
}
