package workflow

import (
	"context"
	"os"
	"path/filepath"
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
	if req.Description != "Run workflow review: 4 nodes, up to 4 subagent turns, max 4 parallel" {
		t.Fatalf("description = %q", req.Description)
	}

	info := runToCompletion(t, wt, params)
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

const orchestrator = "---\nname: split\n---\n```mermaid\nflowchart LR\n  plan --> review --> merge\n```\n\n## plan\nSplit it up\n\n## review\nfor_each: plan.tasks\nmax_workers: 4\nmode: explore\n\nReview {{item.prompt}}\n\n## merge\nMerge {{review}}\n"

func TestWorkflowToolStatesTheWorstCase(t *testing.T) {
	wt := NewWorkflowTool()
	wt.SetExecutor(&scriptedExecutor{})
	req, err := wt.PreparePermission(context.Background(), map[string]any{"definition": orchestrator}, ".")
	if err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	want := "Run workflow split: 3 nodes, up to 6 subagent turns, max 4 parallel\nreview: up to 4 workers over plan.tasks"
	if req.Description != want {
		t.Fatalf("description = %q,\nwant %q", req.Description, want)
	}
}

func TestWorkflowToolFansOutOverAPlan(t *testing.T) {
	exec := &scriptedExecutor{outputs: map[string]string{
		"plan":        `{"tasks":[{"name":"llm","prompt":"errors"},{"name":"tool","prompt":"perms"}]}`,
		"review·llm":  "L",
		"review·tool": "T",
		"merge":       "done",
	}}
	wt := NewWorkflowTool()
	wt.SetExecutor(exec)
	params := map[string]any{"definition": orchestrator}
	if _, err := wt.PreparePermission(context.Background(), params, "."); err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	info := runToCompletion(t, wt, params)

	if info.Status != task.StatusCompleted {
		t.Fatalf("status = %s (%s)", info.Status, info.Error)
	}
	byNode := map[string]tool.AgentExecRequest{}
	exec.mu.Lock()
	for _, r := range exec.reqs {
		byNode[r.Description] = r
	}
	exec.mu.Unlock()
	if got := byNode["split/review·llm"]; got.Prompt != "Review errors" || got.Mode != "explore" {
		t.Fatalf("llm worker = %+v", got)
	}
	if got := byNode["split/merge"].Prompt; !strings.Contains(got, "## llm\nL") || !strings.Contains(got, "## tool\nT") {
		t.Fatalf("merge prompt = %q", got)
	}
}

func TestWorkflowToolRunsASavedWorkflow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExecutor{outputs: map[string]string{"diff": "D", "sec": "S", "perf": "P", "report": "R"}}
	wt := NewWorkflowTool()
	wt.SetExecutor(exec)
	wt.SetSearchPaths([]string{dir})

	if list := wt.Schema().Description; !strings.Contains(list, "Saved workflows") || !strings.Contains(list, "- review") {
		t.Fatalf("schema does not list the saved workflow:\n%s", list)
	}

	params := map[string]any{"name": "review", "inputs": map[string]any{"base": "main"}}
	if _, err := wt.PreparePermission(context.Background(), params, "."); err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	info := runToCompletion(t, wt, params)
	if info.Status != task.StatusCompleted || !strings.Contains(info.Output, "## report\nR") {
		t.Fatalf("status = %s output:\n%s", info.Status, info.Output)
	}
}

func TestWorkflowToolNameErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := NewWorkflowTool()
	wt.SetExecutor(&scriptedExecutor{})
	wt.SetSearchPaths([]string{dir})

	_, err := wt.PreparePermission(context.Background(), map[string]any{"name": "nope"}, ".")
	if err == nil || !strings.Contains(err.Error(), "no workflow named nope") || !strings.Contains(err.Error(), "- review") {
		t.Fatalf("err = %v, want it to list what is saved", err)
	}
	if _, err := wt.PreparePermission(context.Background(), map[string]any{}, "."); err == nil || !strings.Contains(err.Error(), "name or definition is required") {
		t.Fatalf("empty call err = %v", err)
	}
	if _, err := wt.PreparePermission(context.Background(), map[string]any{"name": "review", "definition": definition}, "."); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both err = %v", err)
	}
}

// runToCompletion executes an approved call and waits for its background task.
func runToCompletion(t *testing.T, wt *WorkflowTool, params map[string]any) task.TaskInfo {
	t.Helper()
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
	return bg.GetStatus()
}

const optimizer = "---\nname: polish\n---\n```mermaid\nflowchart LR\n  draft --> review\n  review -->|FAIL x3| draft\n  review -->|PASS| ship\n```\n\n## draft\nWrite it. Last review: {{review}}\n\n## review\nPASS or FAIL: {{draft}}\n\n## ship\nShip {{draft}}\n"

func TestWorkflowToolStatesLoopRounds(t *testing.T) {
	wt := NewWorkflowTool()
	wt.SetExecutor(&scriptedExecutor{})
	req, err := wt.PreparePermission(context.Background(), map[string]any{"definition": optimizer}, ".")
	if err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	want := "Run workflow polish: 7 nodes, up to 7 subagent turns, max 4 parallel\ndraft/review: up to 3 rounds"
	if req.Description != want {
		t.Fatalf("description = %q,\nwant %q", req.Description, want)
	}
}

func TestWorkflowToolRetriesUntilReviewPasses(t *testing.T) {
	exec := &scriptedExecutor{outputs: map[string]string{
		"draft#1": "v1", "review#1": "FAIL",
		"draft#2": "v2", "review#2": "PASS",
		"ship": "shipped",
	}}
	wt := NewWorkflowTool()
	wt.SetExecutor(exec)
	params := map[string]any{"definition": optimizer}
	if _, err := wt.PreparePermission(context.Background(), params, "."); err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	info := runToCompletion(t, wt, params)

	if info.Status != task.StatusCompleted {
		t.Fatalf("status = %s (%s)\n%s", info.Status, info.Error, info.Output)
	}
	byNode := map[string]tool.AgentExecRequest{}
	exec.mu.Lock()
	for _, r := range exec.reqs {
		byNode[r.Description] = r
	}
	exec.mu.Unlock()
	if got := byNode["polish/draft#2"].Prompt; got != "Write it. Last review: FAIL" {
		t.Fatalf("second draft prompt = %q", got)
	}
	if got := byNode["polish/ship"].Prompt; got != "Ship v2" {
		t.Fatalf("ship prompt = %q", got)
	}
	if _, ran := byNode["polish/draft#3"]; ran {
		t.Fatal("round 3 ran although round 2 passed")
	}
}
