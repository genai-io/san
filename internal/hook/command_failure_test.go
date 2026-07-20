package hook

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func engineRunning(t *testing.T, command string) *Engine {
	t.Helper()
	data := setting.NewData()
	data.Hooks = map[string][]setting.Hook{
		string(PreToolUse): {{
			Matcher: "Bash",
			Hooks:   []setting.HookCmd{{Type: "command", Command: command}},
		}},
	}
	return NewEngine(data, "test-session", t.TempDir(), "")
}

// A hook that never worked — a typo, a missing interpreter — used to be
// indistinguishable from one that succeeded: Error stayed nil, so Execute took
// neither the warn branch nor the audit-error branch and the run was recorded
// as "ran", with stderr discarded. The user got nothing.
func TestNonZeroExitIsReportedNotSwallowed(t *testing.T) {
	engine := engineRunning(t, "echo 'jq: command not found' >&2; exit 127")

	var audited []HookFiredAudit
	engine.SetAuditCallback(func(a HookFiredAudit) { audited = append(audited, a) })

	outcome := engine.Execute(context.Background(), PreToolUse, HookInput{ToolName: "Bash"})

	if outcome.ShouldContinue != true {
		t.Error("a failed hook is not a blocking one; the turn should carry on")
	}
	if len(audited) != 1 {
		t.Fatalf("expected one audit record, got %d", len(audited))
	}
	if audited[0].Outcome != outcomeError {
		t.Errorf("audit outcome = %q, want %q — the failure was recorded as success",
			audited[0].Outcome, outcomeError)
	}
	if !strings.Contains(audited[0].Reason, "command not found") {
		t.Errorf("audit reason = %q, want the hook's stderr", audited[0].Reason)
	}
}

// Exit 2 is the blocking convention and keeps its own path.
func TestExitTwoStillBlocks(t *testing.T) {
	engine := engineRunning(t, "echo 'not allowed' >&2; exit 2")

	outcome := engine.Execute(context.Background(), PreToolUse, HookInput{ToolName: "Bash"})

	if outcome.ShouldContinue {
		t.Error("exit 2 should block the tool call")
	}
	if !strings.Contains(outcome.BlockReason, "not allowed") {
		t.Errorf("BlockReason = %q, want the hook's stderr", outcome.BlockReason)
	}
}

// A hook that succeeds must stay clean — no error, no audit noise.
func TestZeroExitStaysSuccessful(t *testing.T) {
	engine := engineRunning(t, "exit 0")

	var audited []HookFiredAudit
	engine.SetAuditCallback(func(a HookFiredAudit) { audited = append(audited, a) })

	outcome := engine.Execute(context.Background(), PreToolUse, HookInput{ToolName: "Bash"})

	if !outcome.ShouldContinue {
		t.Error("a successful hook should not stop the turn")
	}
	if len(audited) != 1 || audited[0].Outcome != outcomeRan {
		t.Errorf("audit = %+v, want a single %q record", audited, outcomeRan)
	}
}
