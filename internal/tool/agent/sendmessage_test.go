package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/group"

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
		"message": "check the auth module too",
	}, ".")

	if !result.Success {
		t.Fatalf("expected delivery to succeed: %s", result.Error)
	}
	if got.From != broker.Main || got.To != "task-1" {
		t.Fatalf("addressing wrong: from=%q to=%q", got.From, got.To)
	}
	if !strings.Contains(got.Content, "check the auth module too") ||
		!strings.Contains(got.Content, `<agent-message from="main">`) {
		t.Fatalf("delivered content malformed: %q", got.Content)
	}
}

func TestSendMessage_SubagentReportsToMain(t *testing.T) {
	broker.Reset()
	t.Cleanup(broker.Reset)

	var got broker.Message
	broker.Register(broker.Main, func(m broker.Message) bool { got = m; return true })

	ctx := tool.WithAgentID(context.Background(), "task-9")
	result := NewSendMessageTool().Execute(ctx, map[string]any{
		"to":      "main",
		"message": "found the root cause",
	}, ".")

	if !result.Success {
		t.Fatalf("expected report to succeed: %s", result.Error)
	}
	if got.From != "task-9" || got.To != broker.Main {
		t.Fatalf("addressing wrong: from=%q to=%q", got.From, got.To)
	}
	if !strings.Contains(got.Content, `<agent-message from="task-9">`) {
		t.Fatalf("sender envelope missing: %q", got.Content)
	}
}

func TestSendMessage_UnregisteredRecipientErrors(t *testing.T) {
	broker.Reset()
	t.Cleanup(broker.Reset)

	result := NewSendMessageTool().Execute(context.Background(), map[string]any{
		"to":      "task-gone",
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

func TestSendMessage_GroupMembers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group.BindSession("s-self")
	if _, err := group.Join("shop", group.Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = group.Leave() })
	home, _ := os.UserHomeDir()
	web := group.Member{Name: "web", Mode: group.Passive, SessionID: "s-web"}
	dir := filepath.Join(home, ".san", "groups", "shop")
	if err := os.MkdirAll(filepath.Join(dir, "web.inbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.WriteJSON(filepath.Join(dir, "web.json"), web, 0o600); err != nil {
		t.Fatal(err)
	}

	sm := NewSendMessageTool()
	params := map[string]any{"to": "web", "message": "schema changed"}
	res := sm.Execute(context.Background(), params, "")
	if !res.Success || !strings.HasPrefix(res.Output, "Queued for @web (offline)") {
		t.Errorf("to an offline member: %+v", res)
	}

	sub := tool.WithAgentID(context.Background(), "task-1")
	if res := sm.Execute(sub, params, ""); res.Success || !strings.Contains(res.Output+res.Error, "only the main conversation") {
		t.Errorf("a subagent reached a member: %+v", res)
	}
}
