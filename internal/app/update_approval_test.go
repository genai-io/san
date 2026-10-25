package app

import (
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/perm"
)

func TestMainApprovalCarriesExecutionContext(t *testing.T) {
	m := interactionModel()
	m.env.CWD = t.TempDir()
	m.env.SessionPermissions = setting.NewSessionPermissions()
	request := m.preparePermissionRequest(&agent.PermGateRequest{RequestID: "main-context", ToolName: "Bash", Input: map[string]any{"command": "touch fixture.txt"}})
	if request.WorkingDirectory != m.env.CWD || request.PermissionMode != m.env.SessionMode() {
		t.Fatalf("main approval lost execution context: cwd=%q mode=%q", request.WorkingDirectory, request.PermissionMode)
	}
	if request.ID != "main-context" || request.BashMeta == nil || request.BashMeta.Command != "touch fixture.txt" {
		t.Fatal("execution context displaced the rich tool preview or request identity")
	}
}

func TestPermDecisionRecordSnapshotsInput(t *testing.T) {
	input := map[string]any{"prompt": "inspect the repository"}
	req := &agent.PermGateRequest{
		RequestID: "permission-1",
		ToolName:  "Agent",
		Input:     input,
	}

	record := permDecisionRecord(req, permissionDecision{
		Approved: true,
		Request:  &perm.PermissionRequest{ToolName: "Agent"},
	}, "user approved", "normal")

	// Agent execution decorates this same input map after the permission gate
	// opens. The permission record must already own serialized bytes by then.
	input["_onActivity"] = func() {}
	input["prompt"] = "changed after approval"

	if got, want := string(record.Input), `{"prompt":"inspect the repository"}`; got != want {
		t.Fatalf("permission input snapshot = %s, want %s", got, want)
	}
}
