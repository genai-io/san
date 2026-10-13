package tool

import (
	"strings"
	"testing"
)

func TestAgentInteractionLabelKeepsIdentityAndEscapesControls(t *testing.T) {
	source := AgentInteractionSource{AgentID: "agent-id", TaskID: "task-id", Name: "worker\n\x1b[2J"}
	label := source.Label()
	if strings.ContainsAny(label, "\n\r\x1b") || !strings.Contains(label, "task-id") || !strings.Contains(label, `\n`) {
		t.Fatalf("unsafe or unattributed label: %q", label)
	}
	if got := (AgentInteractionSource{AgentID: "agent-id", Name: "worker"}).Label(); got != "worker (agent-id)" {
		t.Fatalf("foreground label = %q", got)
	}
	if got := (AgentInteractionSource{}).Label(); got != "" {
		t.Fatalf("main interaction acquired a worker label: %q", got)
	}
}
