package hook

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func TestCommandHookOutput(t *testing.T) {
	const plain = "echo 'PONYTAIL MODE ACTIVE'; echo 'Keep changes focused.'"
	for _, tc := range []struct {
		name    string
		event   EventType
		command string
		context string
		audit   string
		reason  string
	}{
		{"session start text", SessionStart, plain, "PONYTAIL MODE ACTIVE\nKeep changes focused.", outcomeRan, ""},
		{"prompt submit text", UserPromptSubmit, "echo 'Prompt context'", "Prompt context", outcomeRan, ""},
		{"structured output", SessionStart, `echo '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"Structured context"}}'`, "Structured context", outcomeRan, ""},
		{"empty output", SessionStart, "exit 0", "", outcomeRan, ""},
		{"malformed object", SessionStart, `echo '{"systemMessage":'`, "", outcomeError, "hook output not valid JSON"},
		{"permission text stays invalid", PermissionRequest, "echo 'allow'", "", outcomeError, "hook output not valid JSON"},
		{"failed command discards stdout", SessionStart, plain + "; exit 1", "", outcomeError, "hook exited 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := setting.NewData()
			data.Hooks = map[string][]setting.Hook{string(tc.event): {{
				Hooks: []setting.HookCmd{{Type: "command", Command: tc.command}},
			}}}
			engine := NewEngine(data, "test-session", t.TempDir(), "")
			var audits []HookFiredAudit
			engine.SetAuditCallback(func(a HookFiredAudit) { audits = append(audits, a) })

			result := engine.Execute(t.Context(), tc.event, HookInput{Source: "startup", ToolName: "Bash"})
			if got := strings.ReplaceAll(result.AdditionalContext, "\r\n", "\n"); got != tc.context {
				t.Errorf("context = %q, want %q", got, tc.context)
			}
			if !result.ShouldContinue || result.ShouldBlock || result.PermissionAllow {
				t.Errorf("stdout must not change control flow: %+v", result)
			}
			if len(audits) != 1 || audits[0].Outcome != tc.audit || !strings.Contains(audits[0].Reason, tc.reason) {
				t.Errorf("audit = %+v, want %q with reason %q", audits, tc.audit, tc.reason)
			}
		})
	}
}
