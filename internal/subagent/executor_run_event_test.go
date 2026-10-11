package subagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"

	"github.com/genai-io/san/internal/broker"
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

func TestSubagentToolErrorIsReportedBeforeRunEnds(t *testing.T) {
	var notices []broker.Message
	broker.Register(broker.Main, func(message broker.Message) bool {
		notices = append(notices, message)
		return true
	})
	t.Cleanup(func() { broker.Unregister(broker.Main) })
	run := &preparedRun{req: tool.AgentExecRequest{TaskID: "review-1", Description: "Review dependencies"}}
	run.forwardEvent(sdkagent.ToolEnd{ID: "read-1", Name: "Bash", Args: `{"command":"python3 read.py"}`, Err: errors.New("interpreter denied in explore mode")})
	if len(notices) != 1 {
		t.Fatalf("got %d notices before the next inference, want 1", len(notices))
	}
	if notices[0].From != "review-1" || notices[0].Content != "" || !strings.Contains(notices[0].Subject, "interpreter denied") {
		t.Fatalf("tool error must be a display-only notice with its reason: %+v", notices[0])
	}
	run.forwardEvent(sdkagent.ToolEnd{ID: "read-2", Name: "Bash", Result: sdkagent.TextResult("contents")})
	if len(notices) != 1 {
		t.Fatal("successful tools should not produce error notices")
	}
	run.req.TaskID = ""
	run.hookID = "foreground-review"
	run.forwardEvent(sdkagent.ToolEnd{ID: "read-3", Name: "Read", Err: errors.New("file missing")})
	if len(notices) != 2 || notices[1].From != "foreground-review" || !strings.Contains(notices[1].Subject, "file missing") {
		t.Fatalf("foreground worker hid its error: %+v", notices)
	}
	run.forwardEvent(sdkagent.MessageEnd{Attempt: 1, Err: errors.New("provider unavailable")})
	if len(notices) != 3 || !strings.Contains(notices[2].Subject, "inference attempt 1 failed: provider unavailable") {
		t.Fatalf("inference error stayed hidden before retry: %+v", notices)
	}
	run.forwardEvent(sdkagent.MessageEnd{Err: context.Canceled})
	if len(notices) != 3 {
		t.Fatal("deliberate cancellation should not produce an error notice")
	}
}
