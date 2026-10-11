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

func TestPreparedRunToolErrorForwardsLabelAndCancellation(t *testing.T) {
	var notices []broker.Message
	broker.Register(broker.Main, func(message broker.Message) bool {
		notices = append(notices, message)
		return true
	})
	t.Cleanup(func() { broker.Unregister(broker.Main) })
	var label string
	var gotErr error
	run := &preparedRun{req: tool.AgentExecRequest{
		Description: "Review dependencies",
		OnToolResult: func(_, call, _ string, err error) {
			label, gotErr = call, err
			if len(notices) != 1 {
				t.Fatal("error notice must arrive before the tool-result callback")
			}
		},
	}}
	wantErr := errors.New("permission denied")
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{ID: "read-1", Name: "Read", Args: `{"file_path":"main.go"}`, Err: wantErr})
	if label != "Read(main.go)" || gotErr != wantErr || notices[0].Subject != "Review dependencies: "+label+" failed: "+wantErr.Error() {
		t.Fatalf("tool label differs between callback and notice: label=%q, error=%v, notices=%+v", label, gotErr, notices)
	}

	cancelErr := errors.Join(errors.New("worker stopped"), context.Canceled)
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{ID: "read-2", Name: "Read", Args: `{"file_path":"other.go"}`, Err: cancelErr})
	if label != "Read(other.go)" || gotErr != cancelErr || len(notices) != 1 {
		t.Fatalf("cancellation must still reach the callback without a notice: label=%q, error=%v, notices=%+v", label, gotErr, notices)
	}
}

func TestPreparedRunErrorDelivery(t *testing.T) {
	for _, route := range []string{"absent", "rejected", "accepted"} {
		t.Run(route, func(t *testing.T) {
			broker.Unregister(broker.Main)
			t.Cleanup(func() { broker.Unregister(broker.Main) })
			var notices []broker.Message
			if route != "absent" {
				broker.Register(broker.Main, func(message broker.Message) bool {
					if route == "rejected" {
						return false
					}
					notices = append(notices, message)
					return true
				})
			}
			var activity []string
			callbacks := 0
			assertReported := func() {
				t.Helper()
				callbacks++
				if len(notices)+len(activity) != callbacks {
					t.Fatal("error must be reported once, before its result callback")
				}
			}
			run := &preparedRun{req: tool.AgentExecRequest{
				Description:  "Review source",
				OnActivity:   func(line string) { activity = append(activity, line) },
				OnToolResult: func(_, _, _ string, _ error) { assertReported() },
				OnTextEnd:    func(error) { assertReported() },
			}}
			run.forwardEvent(context.Background(), sdkagent.ToolEnd{Name: "Bash", Args: `{"command":"python3 read.py"}`, Err: errors.New("interpreter denied")})
			run.forwardEvent(context.Background(), sdkagent.MessageEnd{Attempt: 1, Err: errors.New("provider unavailable")})
			want := []string{
				"Review source: Bash(python3 read.py) failed: interpreter denied",
				"Review source: inference attempt 1 failed: provider unavailable",
			}
			if route == "accepted" {
				if len(activity) != 0 || len(notices) != len(want) {
					t.Fatalf("broker delivery duplicated activity: notices=%+v, activity=%v", notices, activity)
				}
				for i, notice := range notices {
					if notice.Subject != want[i] || notice.Content != "" {
						t.Fatalf("notice is not display-only: %+v", notice)
					}
				}
			} else if len(activity) != len(want) || activity[0] != want[0] || activity[1] != want[1] {
				t.Fatalf("undelivered errors hidden from activity: %v", activity)
			}
			if len(run.activity) != 0 {
				t.Fatalf("display errors leaked into parent-visible tool history: %v", run.activity)
			}
		})
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

func TestSubagentToolErrorIsReportedBeforeRunEnds(t *testing.T) {
	var notices []broker.Message
	broker.Register(broker.Main, func(message broker.Message) bool {
		notices = append(notices, message)
		return true
	})
	t.Cleanup(func() { broker.Unregister(broker.Main) })
	run := &preparedRun{req: tool.AgentExecRequest{TaskID: "review-1", Description: "Review dependencies"}}
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{ID: "read-1", Name: "Bash", Args: `{"command":"python3 read.py"}`, Err: errors.New("interpreter denied in explore mode")})
	if len(notices) != 1 {
		t.Fatalf("got %d notices before the next inference, want 1", len(notices))
	}
	if notices[0].From != "review-1" || notices[0].Content != "" || !strings.Contains(notices[0].Subject, "interpreter denied") {
		t.Fatalf("tool error must be a display-only notice with its reason: %+v", notices[0])
	}
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{ID: "read-2", Name: "Bash", Result: sdkagent.TextResult("contents")})
	if len(notices) != 1 {
		t.Fatal("successful tools should not produce error notices")
	}
	run.req.TaskID = ""
	run.hookID = "foreground-review"
	run.forwardEvent(context.Background(), sdkagent.ToolEnd{ID: "read-3", Name: "Read", Err: errors.New("file missing")})
	if len(notices) != 2 || notices[1].From != "foreground-review" || !strings.Contains(notices[1].Subject, "file missing") {
		t.Fatalf("foreground worker hid its error: %+v", notices)
	}
	run.forwardEvent(context.Background(), sdkagent.MessageEnd{Attempt: 1, Err: errors.New("provider unavailable")})
	if len(notices) != 3 || !strings.Contains(notices[2].Subject, "inference attempt 1 failed: provider unavailable") {
		t.Fatalf("inference error stayed hidden before retry: %+v", notices)
	}
	run.forwardEvent(context.Background(), sdkagent.MessageEnd{Err: context.Canceled})
	if len(notices) != 3 {
		t.Fatal("deliberate cancellation should not produce an error notice")
	}
}
