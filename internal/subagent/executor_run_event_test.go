package subagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/tool"
)

func TestDeniedToolActivityPutsReasonBeforeCommand(t *testing.T) {
	command := "go test " + strings.Repeat("./long/package/path ", 10)
	gate := subagentPermissionFunc(PermissionAcceptEdits, nil, nil)
	allowed, reason := gate(context.Background(), "Bash", map[string]any{"command": command})
	if allowed || !strings.Contains(reason, "Bash permission denied") || !strings.Contains(reason, "Accept Edits") || !strings.Contains(reason, "allow_tools") {
		t.Fatalf("denial hid tool, mode or required approval: %v, %q", allowed, reason)
	}
	var progress string
	run := &preparedRun{req: tool.AgentExecRequest{OnActivity: func(line string) { progress = line }}}
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{Name: "Bash", Args: `{"command":"` + command + `"}`, Err: errors.New(reason)})
	if !strings.HasPrefix(progress, "Bash failed: Bash permission denied") || !strings.HasSuffix(progress, "…)") {
		t.Fatalf("command preview displaced the failure reason: %q", progress)
	}
}

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
	run.forwardEvent(context.Background(), sdkagent.ToolStart{ID: "tool-1", Name: "Read", Args: `{"file_path":"main.go"}`})
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{ID: "tool-1", Name: "Read", Args: `{"file_path":"main.go"}`, Result: sdkagent.TextResult("first line\nsecond line")})
	if startedID != "tool-1" || startedCall != "Read(main.go)" || id != "tool-1" || name != "Read(main.go)" || output != "first line\nsecond line" || gotErr != nil {
		t.Fatalf("successful tool result: start=%q %q; end=%q %q %q %v", startedID, startedCall, id, name, output, gotErr)
	}

	wantErr := errors.New("permission denied")
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{ID: "tool-2", Name: "Bash", Args: `{"command":"cat missing.go"}`, Err: wantErr})
	if id != "tool-2" || name != "Bash(cat missing.go)" || output != wantErr.Error() || !errors.Is(gotErr, wantErr) {
		t.Fatalf("failed tool result: %q %q %q %v", id, name, output, gotErr)
	}
}

func TestPreparedRunErrorsStayWithWorker(t *testing.T) {
	var notices []broker.Message
	broker.Register(broker.Main, func(message broker.Message) bool {
		notices = append(notices, message)
		return true
	})
	t.Cleanup(func() { broker.Unregister(broker.Main) })
	var activity []string
	var output string
	var gotErr error
	callbacks := 0
	run := &preparedRun{req: tool.AgentExecRequest{
		Description: "Review dependencies",
		OnActivity:  func(line string) { activity = append(activity, line) },
		OnToolResult: func(_, _, text string, err error) {
			callbacks++
			output, gotErr = text, err
		},
		OnTextEnd: func(err error) { callbacks++; gotErr = err },
	}}
	wantErr := errors.New("permission denied")
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{
		ID: "read-1", Name: "Read", Args: `{"file_path":"main.go"}`,
		Result: sdkagent.TextResult("partial diagnostics"), Err: wantErr,
	})
	if output != "partial diagnostics" || gotErr != wantErr || len(activity) != 0 || len(run.activity) != 1 || run.activity[0] != "Read failed: permission denied · Read(main.go)" {
		t.Fatalf("local tool failure lost its reason or output: activity=%v, output=%q, err=%v", activity, output, gotErr)
	}
	run.forwardEvent(context.Background(), sdkagent.MessageEnd{Attempt: 1, Err: errors.New("provider unavailable")})
	if len(activity) != 1 || !strings.Contains(activity[0], "inference attempt 1 failed") {
		t.Fatalf("retry failure missing from worker progress: %v", activity)
	}
	// A later successful attempt must not turn these intermediate failures into
	// independent parent failures. Details retain them for inspection.
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{Name: "Read", Result: sdkagent.TextResult("recovered")})
	run.forwardEvent(context.Background(), sdkagent.MessageEnd{})
	if gotErr != nil || len(notices) != 0 || len(activity) != 1 || len(run.activity) != 2 || callbacks != 4 {
		t.Fatalf("recovered worker polluted the parent: notices=%v, activity=%v, callbacks=%d", notices, activity, callbacks)
	}
}

func TestPreparedRunCancellationStillReachesCallbacks(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		cancelErr := errors.New("exit code -1")
		if wrapped {
			cancelErr = errors.Join(cancelErr, context.Canceled)
		} else {
			cancel()
		}
		var activity []string
		var gotErr error
		callbacks := 0
		run := &preparedRun{req: tool.AgentExecRequest{
			OnActivity:   func(line string) { activity = append(activity, line) },
			OnToolResult: func(_, _, _ string, err error) { callbacks++; gotErr = err },
			OnTextEnd:    func(err error) { callbacks++; gotErr = err },
		}}
		run.forwardEvent(ctx, sdkagent.ToolEnd{Name: "Bash", Err: cancelErr})
		run.forwardEvent(ctx, sdkagent.MessageEnd{Err: cancelErr})
		cancel()
		if gotErr != cancelErr || callbacks != 2 || len(activity) != 0 {
			t.Fatalf("cancellation must finish callbacks without failure noise: activity=%v, callbacks=%d, err=%v", activity, callbacks, gotErr)
		}
	}
}

func TestPreparedRunDeadlineStillReportsErrors(t *testing.T) {
	broker.Unregister(broker.Main)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	var activity []string
	run := &preparedRun{req: tool.AgentExecRequest{
		OnActivity: func(line string) { activity = append(activity, line) },
	}}
	run.forwardEvent(ctx, sdkagent.ToolEnd{Name: "Bash", Err: errors.New("exit code -1")})
	run.forwardEvent(ctx, sdkagent.MessageEnd{Err: context.DeadlineExceeded})
	if len(activity) != 2 {
		t.Fatalf("run deadlines must not be hidden as deliberate cancellation: %v", activity)
	}
}
