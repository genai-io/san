package subagent

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/tool"
)

func TestInteractionUsesFullConfiguredNameWithoutChangingRuntime(t *testing.T) {
	for _, name := range []string{"", "Full configured Worker 名称"} {
		run := &preparedRun{cfg: &runConfig{config: &AgentConfig{Name: name}, displayName: "Agent"}, hookID: "worker-id"}
		source := run.interactionSource(context.Background())
		want := name
		if want == "" {
			want = "Agent"
		}
		if source.AgentName != want || !strings.Contains(source.Label(), want) {
			t.Fatalf("source=%+v label=%q", source, source.Label())
		}
		if run.cfg.displayName != "Agent" {
			t.Fatal("runtime name changed")
		}
		run.req.Workflow = &tool.WorkflowOrigin{Name: "flow", NodeID: "node", TaskID: "run"}
		if got := run.interactionSource(context.Background()).Label(); got != "workflow flow / node node (run)" {
			t.Fatalf("workflow identity changed: %q", got)
		}
	}
}
