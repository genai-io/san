package subagent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestNativePermissionFactoryReceivesExactRunInteractionSource(t *testing.T) {
	ctx := context.Background()
	store, parent := continuationSessions(t, "owner")
	cwd := t.TempDir()
	path := filepath.Join(cwd, "evidence.txt")
	if err := os.WriteFile(path, []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	d := aitest.New(aitest.Asks(ai.ToolCall{ID: "current-read", Name: tool.ToolRead, Input: `{"file_path":"` + path + `"}`}), aitest.Says("done"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	if err := e.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	workflow := &tool.WorkflowOrigin{TaskID: "workflow-task", Name: "review-workflow", NodeID: "inspect", WorkerLabel: "item-2"}
	run, err := e.prepareRun(ctx, tool.AgentExecRequest{TaskID: "actual-task", Prompt: "inspect", ParentToolCallID: "launch-call", Workflow: workflow, ResolvedAgentConfig: &AgentConfig{Name: "actual-reviewer", PermissionMode: PermissionDefault}})
	if err != nil {
		t.Fatal(err)
	}
	want := run.interactionSource(core.WithToolCallID(ctx, "current-read"))
	calls := 0
	e.ConfigureOwner(nil, Default(), func(child *session.Session, background bool, source tool.AgentInteractionSource) tool.Resolver {
		calls++
		if child == nil || child == parent || background {
			t.Error("permission factory lost actual child ownership")
		}
		if !reflect.DeepEqual(source, want) {
			t.Errorf("permission factory reconstructed incomplete source: got=%+v want=%+v", source, want)
		}
		if source.AgentID == source.TaskID || source.AgentID != run.hookID || source.AgentName != "actual-reviewer" || source.ToolCallID != "current-read" || source.ParentToolCallID != "launch-call" || source.WorkingDirectory != cwd || source.PermissionMode != "default" {
			t.Errorf("lost actual run/source identity: %+v", source)
		}
		if source.Workflow == nil || source.Workflow == workflow {
			t.Error("workflow snapshot not detached")
		} else {
			source.Workflow.NodeID = "changed by factory"
		}
		return &childTestResolver{resolution: perm.Resolution{Decision: "allow", By: "config"}}
	}, nil)
	if _, err := e.runPrepared(ctx, run); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || workflow.NodeID != "inspect" {
		t.Fatalf("factory calls=%d workflow=%+v", calls, workflow)
	}
}
