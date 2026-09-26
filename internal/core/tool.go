package core

import (
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Tool is one thing an agent can do, and it is the SDK's: what the model is
// told, and what answers a call.
//
// It carries no Name or Description of its own. Those are Schema().Name and
// Schema().Description — one fact with one home, where before a tool could
// answer one thing to a caller and declare another to the model.
type Tool = sdkagent.Tool

// ToolSchema is what the model is told a tool takes. It is ai.Schema: the
// same three things — a name, a description, and the JSON Schema itself —
// which is what the SDK sends and what it validates arguments against.
type ToolSchema = ai.Schema

// ToAITools is what an inference is told it may call. Run stays nil: San
// executes tools itself and hands the results back as history, so the SDK is
// never asked to run one.
func ToAITools(schemas []ToolSchema) []ai.Tool {
	out := make([]ai.Tool, len(schemas))
	for i, schema := range schemas {
		out[i] = ai.Tool{Schema: schema}
	}
	return out
}
