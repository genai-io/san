package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestRuntimeIdentityForegroundExposesOnlyTaskRoute(t *testing.T) {
	identity := toolresult.AgentIdentity{ID: "private-hook", Name: "private-name", CallID: "private-call", SessionID: "private-session"}
	a := NewAgentTool()
	a.SetExecutor(&recordingExecutor{runResult: &tool.AgentExecResult{Success: true, Content: "finding", Identity: identity}})
	result := a.Execute(context.Background(), map[string]any{"prompt": "work"}, ".")
	details, ok := result.Details.(toolresult.AgentDetails)
	if !ok || details.Identity != identity {
		t.Fatalf("lost identity: %#v", result.Details)
	}
	if !strings.Contains(result.FormatForLLM(), "Task ID: "+identity.ID) {
		t.Fatal("foreground result has no follow-up route")
	}
	for _, value := range []string{identity.Name, identity.CallID, identity.SessionID} {
		if strings.Contains(result.FormatForLLM(), value) {
			t.Fatalf("identity leaked: %q", result.FormatForLLM())
		}
	}
}
