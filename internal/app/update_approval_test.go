package app

import (
	"testing"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/tool/perm"
)

func TestPermissionDecisionRecordSnapshotsInput(t *testing.T) {
	input := map[string]any{"prompt": "inspect the repository"}
	req := &conv.PermGateRequest{
		RequestID: "permission-1",
		ToolName:  "Agent",
		Input:     input,
	}

	record := permissionDecisionRecord(req, permissionDecision{
		Approved: true,
		Request:  &perm.PermissionRequest{ToolName: "Agent"},
	}, "user approved", "normal")

	// Agent execution decorates this same input map after the permission gate
	// opens. The audit record must already own serialized bytes by then.
	input["_onActivity"] = func() {}
	input["prompt"] = "changed after approval"

	if got, want := string(record.Input), `{"prompt":"inspect the repository"}`; got != want {
		t.Fatalf("permission input snapshot = %s, want %s", got, want)
	}
}
