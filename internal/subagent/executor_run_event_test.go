package subagent

import (
	"errors"
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"

	"github.com/genai-io/san/internal/tool"
)

func TestPreparedRunForwardsCompletedToolResult(t *testing.T) {
	var id, name, output string
	var gotErr error
	var startedID, startedCall string
	run := &preparedRun{req: tool.AgentExecRequest{
		OnToolStart: func(toolID, call string) { startedID, startedCall = toolID, call },
		OnToolResult: func(toolID, call, text string, err error) {
			id, name, output, gotErr = toolID, call, text, err
		},
	}}
	run.forwardEvent(sdkagent.ToolStart{ID: "tool-1", Name: "Read", Args: `{"file_path":"main.go"}`})
	run.forwardEvent(sdkagent.ToolEnd{ID: "tool-1", Name: "Read", Args: `{"file_path":"main.go"}`, Result: sdkagent.TextResult("first line\nsecond line")})
	if startedID != "tool-1" || startedCall != "Read(main.go)" || id != "tool-1" || name != "Read(main.go)" || output != "first line\nsecond line" || gotErr != nil {
		t.Fatalf("successful tool result: start=%q %q; end=%q %q %q %v", startedID, startedCall, id, name, output, gotErr)
	}

	wantErr := errors.New("permission denied")
	run.forwardEvent(sdkagent.ToolEnd{ID: "tool-2", Name: "Bash", Args: `{"command":"cat missing.go"}`, Err: wantErr})
	if id != "tool-2" || name != "Bash(cat missing.go)" || output != wantErr.Error() || !errors.Is(gotErr, wantErr) {
		t.Fatalf("failed tool result: %q %q %q %v", id, name, output, gotErr)
	}
}
