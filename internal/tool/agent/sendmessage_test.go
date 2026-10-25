package agent

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/task"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/tool"
)

func TestSendMessage_DeliversToRegisteredAgent(t *testing.T) {
	broker.Reset()
	t.Cleanup(broker.Reset)

	var got broker.Message
	broker.Register("task-1", func(m broker.Message) bool { got = m; return true })

	result := NewSendMessageTool().Execute(context.Background(), map[string]any{
		"to":      "task-1",
		"subject": "a note",
		"message": "check the auth module too",
	}, ".")

	if !result.Success {
		t.Fatalf("expected delivery to succeed: %s", result.Error)
	}
	if got.From != broker.Main || got.To != "task-1" {
		t.Fatalf("addressing wrong: from=%q to=%q", got.From, got.To)
	}
	if !strings.Contains(got.Content, "check the auth module too") ||
		!strings.Contains(got.Content, `<agent-message from="main" `) {
		t.Fatalf("delivered content malformed: %q", got.Content)
	}
}

func TestSendMessage_SubagentReportsToMain(t *testing.T) {
	broker.Reset()
	t.Cleanup(broker.Reset)

	var got broker.Message
	broker.Register(broker.Main, func(m broker.Message) bool { got = m; return true })

	ctx := tool.WithAgentID(task.WithSessionID(context.Background(), "parent-session"), "task-9")
	result := NewSendMessageTool().Execute(ctx, map[string]any{
		"to":      "main",
		"subject": `blocked "test" & <build>`,
		"message": "found the root cause",
	}, ".")

	if !result.Success {
		t.Fatalf("expected report to succeed: %s", result.Error)
	}
	if got.From != "task-9" || got.To != broker.Main {
		t.Fatalf("addressing wrong: from=%q to=%q", got.From, got.To)
	}
	if got.SessionID != "parent-session" {
		t.Fatalf("worker message lost its owning session: %+v", got)
	}
	if !strings.Contains(got.Content, `<agent-message from="task-9" `) {
		t.Fatalf("sender envelope missing: %q", got.Content)
	}
	var decoded struct {
		Subject string `xml:"subject,attr"`
	}
	if err := xml.Unmarshal([]byte(got.Content), &decoded); err != nil || decoded.Subject != got.Subject {
		t.Fatalf("model envelope lost its display subject: subject=%q, decoded=%+v, err=%v", got.Subject, decoded, err)
	}
}

func TestSendMessage_UnregisteredRecipientErrors(t *testing.T) {
	broker.Reset()
	t.Cleanup(broker.Reset)

	result := NewSendMessageTool().Execute(context.Background(), map[string]any{
		"to":      "task-gone",
		"subject": "a note",
		"message": "anyone home?",
	}, ".")

	if result.Success {
		t.Fatal("sending to an unregistered address should fail")
	}
	if !strings.Contains(result.Error, "spawn a new agent") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}

func TestSendMessage_SelfSendRejected(t *testing.T) {
	broker.Reset()
	t.Cleanup(broker.Reset)
	broker.Register("task-7", func(broker.Message) bool { return true })

	ctx := tool.WithAgentID(context.Background(), "task-7")
	result := NewSendMessageTool().Execute(ctx, map[string]any{
		"to":      "task-7",
		"subject": "a note",
		"message": "hello me",
	}, ".")

	if result.Success {
		t.Fatal("a self-addressed message should be rejected")
	}
	if !strings.Contains(result.Error, "yourself") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}

func TestSendMessage_RequiresRecipientAndBody(t *testing.T) {
	broker.Reset()
	t.Cleanup(broker.Reset)
	toolInst := NewSendMessageTool()

	if r := toolInst.Execute(context.Background(), map[string]any{"to": "x"}, "."); r.Success {
		t.Fatal("missing message should fail")
	}
	if r := toolInst.Execute(context.Background(), map[string]any{"message": "hi"}, "."); r.Success {
		t.Fatal("missing recipient should fail")
	}
}

func TestSendMessage_KnownTaskBoundary(t *testing.T) {
	cases := []struct {
		name, owner, sender string
		status              task.TaskStatus
		bash                bool
		wantError           string
	}{
		{name: "running", owner: "session-a", sender: "session-a", status: task.StatusRunning},
		{name: "legacy sender", owner: "session-a", status: task.StatusRunning},
		{name: "legacy owner", sender: "session-a", status: task.StatusRunning},
		{name: "completed stale route", status: task.StatusCompleted, wantError: "resume_task_id"},
		{name: "failed stale route", status: task.StatusFailed, wantError: "same task and session"},
		{name: "stopped stale route", status: task.StatusStopped, wantError: "cannot be resumed automatically"},
		{name: "killed stale route", status: task.StatusKilled, wantError: "deliberately stopped"},
		{name: "bash", status: task.StatusRunning, bash: true, wantError: "not an agent"},
		{name: "cross session", owner: "session-a", sender: "session-b", status: task.StatusRunning, wantError: "different parent session"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			broker.Reset()
			t.Cleanup(broker.Reset)
			id := task.NewID()
			owner := task.WithSessionID(context.Background(), tc.owner)
			if tc.bash {
				bg := task.Default().CreateBashTask(owner, &exec.Cmd{Process: &os.Process{Pid: 1}}, "true", "test", func() {})
				id = bg.GetID()
			} else {
				bg := task.Default().CreateAgentTask(id, "worker", "test", owner, func() {})
				switch tc.status {
				case task.StatusCompleted:
					bg.Complete(nil)
				case task.StatusFailed:
					bg.Complete(fmt.Errorf("test failure"))
				case task.StatusStopped:
					bg.Complete(context.Canceled)
				case task.StatusKilled:
					if err := bg.Kill(); err != nil {
						t.Fatal(err)
					}
				}
				if got := bg.GetStatus().Status; got != tc.status {
					t.Fatalf("fixture status = %s, want %s", got, tc.status)
				}
			}
			t.Cleanup(func() { task.Default().Remove(id) })
			calls := 0
			broker.Register(id, func(m broker.Message) bool {
				calls++
				if m.To != id {
					t.Fatalf("retargeted to %q", m.To)
				}
				return true
			})
			ctx := task.WithSessionID(context.Background(), tc.sender)
			result := NewSendMessageTool().Execute(ctx, map[string]any{"to": id, "message": "follow up"}, ".")
			if tc.wantError != "" {
				if result.Success || !strings.Contains(result.Error, tc.wantError) || calls != 0 {
					t.Fatalf("result=%+v, broker calls=%d", result, calls)
				}
			} else if !result.Success || calls != 1 || !strings.Contains(result.Output, "accepted and queued") || !strings.Contains(result.Output, "not an acknowledgment or completion") {
				t.Fatalf("result=%+v, broker calls=%d", result, calls)
			}
		})
	}
}

func TestSendMessage_GroupMembers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group.BindSession(func() string { return "s-self" })
	if _, err := group.Join("shop", group.Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = group.Leave() })
	home, _ := os.UserHomeDir()
	web := group.Member{Name: "web", Hold: true, SessionID: "s-web"}
	dir := filepath.Join(home, ".san", "groups", "shop")
	if err := os.MkdirAll(filepath.Join(dir, "web.inbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.WriteJSON(filepath.Join(dir, "web.json"), web, 0o600); err != nil {
		t.Fatal(err)
	}

	sm := NewSendMessageTool()
	params := map[string]any{"to": "web", "subject": "schema changed", "message": "orders gained coupon_code"}
	res := sm.Execute(context.Background(), params, "")
	if !res.Success || !strings.HasPrefix(res.Output, "Queued · offline") {
		t.Errorf("to an offline member: %+v", res)
	}

	sub := tool.WithAgentID(context.Background(), "task-1")
	if res := sm.Execute(sub, params, ""); res.Success || !strings.Contains(res.Output+res.Error, "only the main conversation") {
		t.Errorf("a subagent reached a member: %+v", res)
	}
}
