package workflow

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
)

// scriptedExecutor answers each node by its description suffix and records
// what it was asked to run.
type scriptedExecutor struct {
	mu       sync.Mutex
	reqs     []tool.AgentExecRequest
	outputs  map[string]string // node id -> content
	disabled string
	// unknown is a name the registry does not hold; it resolves to a display
	// label with no Source, the way subagent.Registry answers a typo.
	unknown string
}

func (e *scriptedExecutor) Run(_ context.Context, req tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	e.mu.Lock()
	e.reqs = append(e.reqs, req)
	e.mu.Unlock()
	id := req.Description[strings.LastIndex(req.Description, "/")+1:]
	return &tool.AgentExecResult{Success: true, Content: e.outputs[id], StepCount: 1}, nil
}
func (e *scriptedExecutor) RunBackground(tool.AgentExecRequest) (tool.AgentTaskInfo, error) {
	panic("workflow nodes run in the foreground of their own goroutine")
}
func (e *scriptedExecutor) GetAgentConfig(name string) (tool.AgentConfigInfo, bool) {
	info, _, ok := e.ResolveAgentSelection(name)
	return info, ok
}
func (e *scriptedExecutor) ResolveAgentSelection(name string) (tool.AgentConfigInfo, any, bool) {
	switch {
	case name != "" && name == e.disabled:
		return tool.AgentConfigInfo{}, nil, false
	case name != "" && name == e.unknown:
		return tool.AgentConfigInfo{Name: name}, "cfg:" + name, true
	}
	return tool.AgentConfigInfo{Name: name, Source: "project"}, "cfg:" + name, true
}
func (e *scriptedExecutor) GetParentModelID() string { return "parent" }

const definition = "---\nname: review\n---\n```mermaid\nflowchart LR\n  diff --> sec & perf --> report\n```\n\n## diff\nagent: explorer\n\nSummarize {{input.base}}..HEAD\n\n## sec\nmode: explore\n\nSecurity: {{diff}}\n\n## perf\nmode: explore\n\nPerf: {{diff}}\n\n## report\nMerge {{sec}} {{perf}}\n"

func TestWorkflowToolRunsNodesThroughTheExecutor(t *testing.T) {
	exec := &scriptedExecutor{outputs: map[string]string{"diff": "D", "sec": "S", "perf": "P", "report": "R"}}
	wt := NewWorkflowTool()
	wt.SetExecutor(exec)
	params := map[string]any{"definition": definition, "inputs": map[string]any{"base": "main"}}

	req, err := wt.PreparePermission(context.Background(), params, ".")
	if err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	if req.Description != "Run workflow review: 4 nodes, max 4 parallel" {
		t.Fatalf("description = %q", req.Description)
	}

	result := wt.ExecuteApproved(context.Background(), params, ".")
	if !result.Success {
		t.Fatalf("Execute: %s", result.Error)
	}
	id := result.Output[strings.Index(result.Output, "Task ID: ")+len("Task ID: "):]
	id = id[:strings.Index(id, "\n")]
	bg, ok := task.Default().Get(id)
	if !ok {
		t.Fatalf("task %s not registered", id)
	}
	if !bg.WaitForCompletion(5 * time.Second) {
		t.Fatal("workflow did not finish")
	}

	info := bg.GetStatus()
	if info.Status != task.StatusCompleted {
		t.Fatalf("status = %s (%s)", info.Status, info.Error)
	}
	if !strings.Contains(info.Output, "workflow review succeeded") || !strings.Contains(info.Output, "## report\nR") {
		t.Fatalf("output:\n%s", info.Output)
	}
	if info.StepCount != 4 {
		t.Fatalf("steps = %d, want one per node", info.StepCount)
	}

	byNode := map[string]tool.AgentExecRequest{}
	for _, r := range exec.reqs {
		byNode[r.Description] = r
	}
	diff := byNode["review/diff"]
	if diff.Agent != "explorer" || diff.Prompt != "Summarize main..HEAD" || !diff.Background {
		t.Fatalf("diff request = %+v", diff)
	}
	if sec := byNode["review/sec"]; sec.Mode != "explore" || sec.Prompt != "Security: D" {
		t.Fatalf("sec request = %+v", sec)
	}
	if report := byNode["review/report"]; report.Prompt != "Merge S P" {
		t.Fatalf("report prompt = %q", report.Prompt)
	}
}

func TestWorkflowToolRejectsBeforeRunning(t *testing.T) {
	wt := NewWorkflowTool()
	wt.SetExecutor(&scriptedExecutor{disabled: "explorer"})

	if _, err := wt.PreparePermission(context.Background(), map[string]any{"definition": definition}, "."); err == nil || !strings.Contains(err.Error(), `node diff: agent "explorer" is disabled`) {
		t.Fatalf("disabled agent err = %v", err)
	}
	broken := strings.Replace(definition, "{{diff}}", "{{dif}}", 1)
	if _, err := wt.PreparePermission(context.Background(), map[string]any{"definition": broken}, "."); err == nil || !strings.Contains(err.Error(), "invalid workflow") {
		t.Fatalf("invalid definition err = %v", err)
	}
}

func TestWorkflowToolRejectsATypoedAgentName(t *testing.T) {
	wt := NewWorkflowTool()
	wt.SetExecutor(&scriptedExecutor{unknown: "Explorr"})
	src := strings.Replace(definition, "agent: explorer", "agent: Explorr", 1)

	_, err := wt.PreparePermission(context.Background(), map[string]any{"definition": src}, ".")
	if err == nil || !strings.Contains(err.Error(), `node diff: no agent named "Explorr"`) {
		t.Fatalf("err = %v; a written-down name that resolves to nothing is a typo, not a label", err)
	}
	if !strings.Contains(err.Error(), "mode: explore|edit") {
		t.Fatalf("err = %v; it should say what to write instead", err)
	}
}

func TestWorkflowToolAcceptsANodeWithNoAgent(t *testing.T) {
	wt := NewWorkflowTool()
	wt.SetExecutor(&scriptedExecutor{})
	src := strings.Replace(definition, "agent: explorer\n", "", 1)

	if _, err := wt.PreparePermission(context.Background(), map[string]any{"definition": src}, "."); err != nil {
		t.Fatalf("a node without an agent takes the default one: %v", err)
	}
}
