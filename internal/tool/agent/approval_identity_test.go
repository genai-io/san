package agent

import (
	"context"
	"testing"
)

func TestApprovalCanonicalAgentName(t *testing.T) {
	for _, name := range []string{"", "Full configured Worker 名称"} {
		worker := NewAgentTool()
		worker.SetExecutor(&recordingExecutor{configOK: true})
		req, err := worker.PreparePermission(context.Background(), map[string]any{"name": name, "prompt": "Inspect"}, ".")
		if err != nil {
			t.Fatal(err)
		}
		want := name
		if want == "" {
			want = "Agent"
		}
		if req.AgentMeta.AgentName != want {
			t.Fatalf("name=%q want %q", req.AgentMeta.AgentName, want)
		}
	}
}
