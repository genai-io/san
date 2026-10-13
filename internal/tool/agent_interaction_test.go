package tool

import (
	"strings"
	"testing"
)

func TestWorkflowInteractionLabel(t *testing.T) {
	source := AgentInteractionSource{AgentID: "worker-id", AgentName: "agent", Workflow: &WorkflowOrigin{
		TaskID: "run-id", Name: "review\n", NodeID: "check\x1b", WorkerLabel: "item\r",
	}}
	label := source.Label()
	for _, want := range []string{"workflow review", "node check", "worker item", "run-id", `\n`, `\r`, `\x1b`} {
		if !strings.Contains(label, want) {
			t.Errorf("label %q missing %q", label, want)
		}
	}
	if strings.ContainsAny(label, "\n\r\x1b") {
		t.Fatalf("unsafe label: %q", label)
	}
}

func TestAgentInteractionLabelKeepsIdentityAndEscapesControls(t *testing.T) {
	source := AgentInteractionSource{AgentID: "agent-id", TaskID: "task-id", AgentName: "worker\n\x1b[2J"}
	label := source.Label()
	if strings.ContainsAny(label, "\n\r\x1b") || !strings.Contains(label, "task-id") || !strings.Contains(label, `\n`) {
		t.Fatalf("unsafe or unattributed label: %q", label)
	}
	if got := (AgentInteractionSource{AgentID: "agent-id", AgentName: "worker"}).Label(); got != "worker (agent-id)" {
		t.Fatalf("foreground label = %q", got)
	}
	if got := (AgentInteractionSource{}).Label(); got != "" {
		t.Fatalf("main interaction acquired a worker label: %q", got)
	}
}
