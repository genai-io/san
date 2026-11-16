package flow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	flowmodel "github.com/genai-io/san/internal/flow"
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
	started  chan struct{}
	release  chan struct{}
	// unknown is a name the registry does not hold; it resolves to a display
	// label with no Source, the way subagent.Registry answers a typo.
	unknown    string
	activity   string
	toolResult string
	toolEvents func(tool.AgentExecRequest)
}

func (e *scriptedExecutor) Run(_ context.Context, req tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	e.mu.Lock()
	e.reqs = append(e.reqs, req)
	e.mu.Unlock()
	if e.activity != "" && req.OnActivity != nil {
		req.OnActivity(e.activity)
	}
	if e.toolResult != "" && req.OnToolResult != nil {
		req.OnToolStart("call-1", "Read(example.go)")
		req.OnToolResult("call-1", "Read(example.go)", e.toolResult, nil)
	}
	if e.toolEvents != nil {
		e.toolEvents(req)
	}
	id := req.Description[strings.LastIndex(req.Description, "/")+1:]
	if e.started != nil {
		close(e.started)
		<-e.release
	}
	return &tool.AgentExecResult{Success: true, Content: e.outputs[id], StepCount: 1}, nil
}
func (e *scriptedExecutor) RunBackground(tool.AgentExecRequest) (tool.AgentTaskInfo, error) {
	panic("flow nodes run in the foreground of their own goroutine")
}
func (e *scriptedExecutor) AgentConfig(name string) (tool.AgentConfigInfo, bool) {
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
func (e *scriptedExecutor) ParentModelID() string { return "parent" }

type unfinishedExecutor struct {
	scriptedExecutor
	result *tool.AgentExecResult
	err    error
}

func (e *unfinishedExecutor) Run(context.Context, tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	return e.result, e.err
}

func TestFlowPreservesUnfinishedResultsAndUsage(t *testing.T) {
	task.Initialize(task.Options{OutputDir: t.TempDir()})
	t.Cleanup(task.ResetDefaultTracker)
	for _, runErr := range []error{errors.New("provider unavailable"), context.Canceled, nil} {
		exec := &unfinishedExecutor{result: &tool.AgentExecResult{
			Content: "verified partial findings", Error: "step budget exhausted", AgentID: "child-1", OutputFile: "/tmp/child-1.jsonl",
			StepCount: 2, TotalInputTokens: 100, TotalOutputTokens: 25,
		}, err: runErr}
		wt := NewFlowTool()
		wt.SetExecutor(exec)
		info := runToCompletion(t, wt, map[string]any{"definition": "```mermaid\nflowchart LR\n  first\n  second\n```\n\n## first\nReview\n\n## second\nReview\n"})
		if info.Status != task.StatusFailed || info.StepCount != 4 || info.TokenUsage != 250 {
			t.Fatalf("unfinished run lost failure or usage: %+v", info)
		}
		for _, want := range []string{"verified partial findings", "child-1", "/tmp/child-1.jsonl", "unfinished"} {
			if !strings.Contains(info.Output, want) {
				t.Fatalf("flow lost %q: %q", want, info.Output)
			}
		}
		stored, err := os.ReadFile(info.OutputFile)
		if err != nil || !strings.Contains(string(stored), "verified partial findings") || !strings.Contains(string(stored), "/tmp/child-1.jsonl") {
			t.Fatalf("unfinished results were not persisted: err=%v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := task.NewAgentTask("flow-1", "flow", "Review", ctx, cancel, "")
	runErr := errors.New("provider unavailable")
	runner := &nodeRunner{exec: &unfinishedExecutor{err: runErr}, task: worker, name: "review"}
	output, err := runner.RunNode(ctx, flowmodel.Call{Node: &flowmodel.Node{ID: "only", Config: map[string]string{}}})
	if output != "" || !errors.Is(err, runErr) {
		t.Fatalf("failure before any result changed: output=%q err=%v", output, err)
	}
}

const definition = "---\nname: review\n---\n```mermaid\nflowchart LR\n  diff --> sec & perf --> report\n```\n\n## diff\nagent: explorer\n\nSummarize {{input.base}}..HEAD\n\n## sec\nmode: explore\n\nSecurity: {{diff}}\n\n## perf\nmode: explore\n\nPerf: {{diff}}\n\n## report\nMerge {{sec}} {{perf}}\n"

func TestNodeRunnerFlowOrigin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bg := task.NewAgentTask("flow-run", "flow", "review", ctx, cancel, "")
	exec := &scriptedExecutor{}
	runner := &nodeRunner{exec: exec, task: bg, name: "review", parentToolCallID: "spawning-call"}
	_, err := runner.RunNode(ctx, flowmodel.Call{Node: &flowmodel.Node{ID: "check", Config: map[string]string{}}, Label: "item-2"})
	if err != nil {
		t.Fatal(err)
	}
	req := exec.reqs[0]
	want := tool.FlowOrigin{TaskID: "flow-run", Name: "review", NodeID: "check", WorkerLabel: "item-2"}
	if req.Flow == nil || *req.Flow != want || req.ParentToolCallID != "spawning-call" || req.TaskID != "" {
		t.Fatalf("wrong attribution or shared broker address: %+v", req)
	}
}

func TestFlowToolRunsNodesThroughTheExecutor(t *testing.T) {
	exec := &scriptedExecutor{outputs: map[string]string{"diff": "D", "sec": "S", "perf": "P", "report": "R"}}
	wt := NewFlowTool()
	wt.SetExecutor(exec)
	params := map[string]any{"definition": definition, "inputs": map[string]any{"base": "main"}}

	req, err := wt.PreparePermission(context.Background(), params, ".")
	if err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	if req.Description != "Run flow review: 4 nodes, up to 4 subagent turns, max 4 parallel" {
		t.Fatalf("description = %q", req.Description)
	}

	info := runToCompletion(t, wt, params)
	if info.Status != task.StatusCompleted {
		t.Fatalf("status = %s (%s)", info.Status, info.Error)
	}
	if !strings.Contains(info.LiveView, "diff") || !strings.Contains(info.LiveView, "report") || strings.Contains(info.LiveView, "Result:") || strings.Contains(info.LiveView, "Activity") {
		t.Fatalf("node result handoff is missing from live view:\n%s", info.LiveView)
	}
	if !strings.Contains(info.Output, "flow review succeeded") || !strings.Contains(info.Output, "## report\nR") {
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

func TestFlowToolPublishesLiveNodeGraph(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	releaseNode := sync.OnceFunc(func() { close(release) })
	defer releaseNode()
	exec := &scriptedExecutor{outputs: map[string]string{"only": "done"}, started: started, release: release, toolResult: "package main\nfunc main() {}\n"}
	wt := NewFlowTool()
	wt.SetExecutor(exec)
	src := "```mermaid\nflowchart LR\n  only\n```\n\n## only\ngo\n"
	result := wt.Execute(context.Background(), map[string]any{"definition": src}, ".")
	if !result.Success {
		t.Fatalf("launch: %+v", result)
	}
	_, rest, _ := strings.Cut(result.Output, "Task ID: ")
	id, _, _ := strings.Cut(rest, "\n")
	bg, ok := task.Default().Get(id)
	if !ok {
		t.Fatalf("task %q not registered", id)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("node did not start")
	}
	if view := bg.GetStatus().LiveView; !strings.Contains(view, "only ◉") || strings.Contains(view, "Read(example.go)") || strings.Contains(view, "package main") || strings.Contains(view, "Activity") {
		t.Fatalf("live view must contain the running graph, not copied tool activity:\n%s", view)
	}
	releaseNode()
	if !bg.WaitForCompletion(5 * time.Second) {
		t.Fatal("flow did not complete")
	}
	if view := bg.GetStatus().LiveView; !strings.Contains(view, "Flow (unnamed) · 1/1 finished") || !strings.Contains(view, "only ●") {
		t.Fatalf("finished graph did not update:\n%s", view)
	}
}

func TestFlowToolPairsParallelResultsWithTheirCalls(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	releaseNode := sync.OnceFunc(func() { close(release) })
	defer releaseNode()
	exec := &scriptedExecutor{
		outputs: map[string]string{"only": "done"}, started: started, release: release,
		toolEvents: func(req tool.AgentExecRequest) {
			req.OnToolStart("first", "Bash(cat first.go)")
			req.OnToolStart("second", "Bash(cat second.go)")
			req.OnToolResult("second", "Bash(cat second.go)", "second result", nil)
			req.OnToolResult("first", "Bash(cat first.go)", "first result", nil)
		},
	}
	// Callback IDs, rather than parent presentation rows, preserve pairing.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, results := map[string]string{}, map[string]string{}
	runner := &nodeRunner{
		exec:        &scriptedExecutor{toolEvents: exec.toolEvents},
		task:        task.NewAgentTask("callback-pairing", "flow", "review", ctx, cancel, ""),
		onToolStart: func(_, _, id, call string) { calls[id] = call },
		onToolResult: func(_, _, id, call, summary string, failed bool) {
			if failed || calls[id] != call {
				t.Errorf("result callback lost call identity: id=%q call=%q failed=%v", id, call, failed)
			}
			results[id] = summary
		},
	}
	if _, err := runner.RunNode(ctx, flowmodel.Call{Node: &flowmodel.Node{ID: "only", Config: map[string]string{}}}); err != nil {
		t.Fatal(err)
	}
	if results["first"] != "first result" || results["second"] != "second result" {
		t.Fatalf("parallel callback results = %v", results)
	}
	wt := NewFlowTool()
	wt.SetExecutor(exec)
	result := wt.Execute(context.Background(), map[string]any{"definition": "```mermaid\nflowchart LR\n  only\n```\n\n## only\ngo\n"}, ".")
	if !result.Success {
		t.Fatalf("launch: %+v", result)
	}
	_, rest, _ := strings.Cut(result.Output, "Task ID: ")
	id, _, _ := strings.Cut(rest, "\n")
	bg, ok := task.Default().Get(id)
	if !ok {
		t.Fatalf("task %q not registered", id)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("node did not start")
	}
	view := bg.GetStatus().LiveView
	for _, copied := range []string{"Bash(cat first.go)", "Bash(cat second.go)", "first result", "second result", "Activity"} {
		if strings.Contains(view, copied) {
			t.Fatalf("tool activity %q was copied into the parent graph:\n%s", copied, view)
		}
	}
	if !strings.Contains(view, "only ◉") {
		t.Fatalf("running graph is missing:\n%s", view)
	}
	releaseNode()
	if !bg.WaitForCompletion(5 * time.Second) {
		t.Fatal("flow did not complete")
	}
}

func TestFlowToolResultSummaryStaysSmallAndReadable(t *testing.T) {
	if got := flowToolResultSummary("hello", nil); got != "hello" {
		t.Fatalf("short result = %q", got)
	}
	if got := flowToolResultSummary("", nil); got != "no output" {
		t.Fatalf("empty result = %q", got)
	}
	if got := flowToolResultSummary("", errors.New("permission denied")); got != "permission denied" {
		t.Fatalf("failed result = %q", got)
	}
	got := flowToolResultSummary("\x1b[31mfirst\x1b[0m\nsecond\n", nil)
	if !strings.Contains(got, "2 lines") || !strings.HasSuffix(got, " · first") || strings.Contains(got, "\x1b") {
		t.Fatalf("multiline result = %q", got)
	}
}

func TestFlowToolParallelStreamsStayOutOfParentGraph(t *testing.T) {
	firstStreaming, secondStreaming, firstDiscarded := make(chan struct{}), make(chan struct{}), make(chan struct{})
	exec := &scriptedExecutor{toolEvents: func(req tool.AgentExecRequest) {
		if strings.HasSuffix(req.Description, "/a") {
			req.OnTextDelta("previous accepted")
			req.OnTextEnd(nil)
			req.OnTextDelta(strings.Repeat("discarded draft ", 16))
			req.OnTextDelta("unflushed tail")
			close(firstStreaming)
			<-secondStreaming
			req.OnTextEnd(errors.New("stream failed"))
			close(firstDiscarded)
			req.OnTextDelta("accepted retry")
			req.OnTextEnd(nil)
		} else {
			<-firstStreaming
			req.OnTextDelta("parallel accepted")
			close(secondStreaming)
			<-firstDiscarded
			req.OnTextEnd(nil)
		}
	}}
	wt := NewFlowTool()
	wt.SetExecutor(exec)
	const definition = "```mermaid\nflowchart LR\n  a\n  b\n```\n\n## a\ngo\n\n## b\ngo\n"
	info := runToCompletion(t, wt, map[string]any{"definition": definition})
	for _, text := range []string{"previous accepted", "accepted retry", "parallel accepted", "discarded draft", "unflushed tail"} {
		if strings.Contains(info.LiveView, text) {
			t.Fatalf("streamed text %q was copied into the parent graph:\n%s", text, info.LiveView)
		}
	}
	if info.Status != task.StatusCompleted || info.StepCount != 2 {
		t.Fatalf("parallel streams did not complete: %+v", info)
	}
}

func TestFlowToolRejectsBeforeRunning(t *testing.T) {
	wt := NewFlowTool()
	wt.SetExecutor(&scriptedExecutor{disabled: "explorer"})

	if _, err := wt.PreparePermission(context.Background(), map[string]any{"definition": definition}, "."); err == nil || !strings.Contains(err.Error(), `node diff: agent "explorer" is disabled`) {
		t.Fatalf("disabled agent err = %v", err)
	}
	broken := strings.Replace(definition, "{{diff}}", "{{dif}}", 1)
	if _, err := wt.PreparePermission(context.Background(), map[string]any{"definition": broken}, "."); err == nil || !strings.Contains(err.Error(), "invalid flow") {
		t.Fatalf("invalid definition err = %v", err)
	}
}

func TestFlowToolRejectsATypoedAgentName(t *testing.T) {
	wt := NewFlowTool()
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

func TestFlowToolAcceptsANodeWithNoAgent(t *testing.T) {
	wt := NewFlowTool()
	wt.SetExecutor(&scriptedExecutor{})
	src := strings.Replace(definition, "agent: explorer\n", "", 1)

	if _, err := wt.PreparePermission(context.Background(), map[string]any{"definition": src}, "."); err != nil {
		t.Fatalf("a node without an agent takes the default one: %v", err)
	}
}

const orchestrator = "---\nname: split\n---\n```mermaid\nflowchart LR\n  plan --> review --> merge\n```\n\n## plan\nSplit it up\n\n## review\nfor_each: plan.tasks\nmax_workers: 4\nmode: explore\n\nReview {{item.prompt}}\n\n## merge\nMerge {{review}}\n"

func TestFlowToolStatesTheWorstCase(t *testing.T) {
	wt := NewFlowTool()
	wt.SetExecutor(&scriptedExecutor{})
	req, err := wt.PreparePermission(context.Background(), map[string]any{"definition": orchestrator}, ".")
	if err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	want := "Run flow split: 3 nodes, up to 6 subagent turns, max 4 parallel\nreview: up to 4 workers over plan.tasks"
	if req.Description != want {
		t.Fatalf("description = %q,\nwant %q", req.Description, want)
	}
}

func TestFlowToolFansOutOverAPlan(t *testing.T) {
	exec := &scriptedExecutor{outputs: map[string]string{
		"plan":        `{"tasks":[{"name":"llm","prompt":"errors"},{"name":"tool","prompt":"perms"}]}`,
		"review·llm":  "L",
		"review·tool": "T",
		"merge":       "done",
	}}
	wt := NewFlowTool()
	wt.SetExecutor(exec)
	params := map[string]any{"definition": orchestrator}
	if _, err := wt.PreparePermission(context.Background(), params, "."); err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	info := runToCompletion(t, wt, params)

	if info.Status != task.StatusCompleted {
		t.Fatalf("status = %s (%s)", info.Status, info.Error)
	}
	if !strings.Contains(info.LiveView, "review") || strings.Contains(info.LiveView, "Result:") || strings.Contains(info.LiveView, "Activity") {
		t.Fatalf("fan-out parent must show only its graph:\n%s", info.LiveView)
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

func TestFlowLaunchHasOriginWithoutSpawningCall(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExecutor{outputs: map[string]string{"diff": "D", "sec": "S", "perf": "P", "report": "R"}}
	wt := NewFlowTool()
	wt.SetExecutor(exec)
	wt.SetSearchPaths([]string{dir})
	_, runID, err := wt.Launch("review", map[string]string{"base": "main"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	bg, ok := task.Default().Get(runID)
	if !ok || !bg.WaitForCompletion(5*time.Second) {
		t.Fatal("slash-launched flow did not finish")
	}
	if len(exec.reqs) != 4 {
		t.Fatalf("executed %d nodes, want 4", len(exec.reqs))
	}
	for _, req := range exec.reqs {
		if req.Flow == nil || req.Flow.TaskID != runID || req.Flow.Name != "review" || req.Flow.NodeID == "" || req.ParentToolCallID != "" || req.TaskID != "" {
			t.Fatalf("slash launch lost flow origin or acquired a shared worker address: %+v", req)
		}
	}
}

func TestFlowToolRunsASavedFlow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExecutor{outputs: map[string]string{"diff": "D", "sec": "S", "perf": "P", "report": "R"}}
	wt := NewFlowTool()
	wt.SetExecutor(exec)
	wt.SetSearchPaths([]string{dir})

	if list := wt.Schema().Description; !strings.Contains(list, "Saved flows") || !strings.Contains(list, "- review") {
		t.Fatalf("schema does not list the saved flow:\n%s", list)
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

func TestFlowToolNameErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := NewFlowTool()
	wt.SetExecutor(&scriptedExecutor{})
	wt.SetSearchPaths([]string{dir})

	_, err := wt.PreparePermission(context.Background(), map[string]any{"name": "nope"}, ".")
	if err == nil || !strings.Contains(err.Error(), "no flow named nope") || !strings.Contains(err.Error(), "- review") {
		t.Fatalf("err = %v, want it to list what is saved", err)
	}
	if _, err := wt.PreparePermission(context.Background(), map[string]any{}, "."); err == nil || !strings.Contains(err.Error(), "name or definition is required") {
		t.Fatalf("empty call err = %v", err)
	}
	if _, err := wt.PreparePermission(context.Background(), map[string]any{"name": "review", "definition": definition}, "."); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both err = %v", err)
	}
}

func TestFlowBareInputPreflight(t *testing.T) {
	dir := t.TempDir()
	body := strings.ReplaceAll(definition, "{{input.base}}", "{{input}}")
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := NewFlowTool()
	wt.SetExecutor(&scriptedExecutor{})
	wt.SetSearchPaths([]string{dir})
	names, err := wt.SavedInputNames("review")
	if err != nil || strings.Join(names, ",") != "input" {
		t.Fatalf("names=%v err=%v", names, err)
	}
	preview, err := wt.Preview("review", 100)
	if err != nil || !strings.Contains(preview, "input") {
		t.Fatalf("preview=%q err=%v", preview, err)
	}
	if _, _, err := wt.Launch("review", nil, 100); err == nil || !strings.Contains(err.Error(), "input") {
		t.Fatalf("missing input: %v", err)
	}
	if _, err := wt.PreparePermission(context.Background(), map[string]any{"name": "review", "inputs": map[string]any{"input": "main"}}, "."); err != nil {
		t.Fatal(err)
	}
}

func TestFlowToolMissingInputQuotesSavedName(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(definition, "name: review", "name: release review", 1)
	if err := os.WriteFile(filepath.Join(dir, "release.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := NewFlowTool()
	wt.SetExecutor(&scriptedExecutor{})
	wt.SetSearchPaths([]string{dir})
	_, _, err := wt.Launch("release review", nil, 100)
	if err == nil || !strings.Contains(err.Error(), `use /flow show "release review"`) {
		t.Fatalf("missing-input command is not quoted: %v", err)
	}
}

// runToCompletion executes an approved call and waits for its background task.
func runToCompletion(t *testing.T, wt *FlowTool, params map[string]any) task.TaskInfo {
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
		t.Fatal("flow did not finish")
	}
	return bg.GetStatus()
}

type switchExecutorOnLaunch func()

func (f switchExecutorOnLaunch) TaskCreated(task.TaskInfo)   { f() }
func (f switchExecutorOnLaunch) TaskCompleted(task.TaskInfo) {}

func TestFlowKeepsExecutorAndSessionFromLaunch(t *testing.T) {
	if err := task.Initialize(task.Options{OutputDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(task.ResetDefaultTracker)
	task.Default().SetSessionID("first")
	first, second := &scriptedExecutor{}, &scriptedExecutor{}
	wt := NewFlowTool()
	wt.SetExecutor(first)
	// Switch after registration, before the background goroutine can start.
	task.SetLifecycleHandler(switchExecutorOnLaunch(func() {
		wt.SetExecutor(second)
		task.Default().SetSessionID("second")
	}))
	t.Cleanup(func() { task.SetLifecycleHandler(nil) })
	info := runToCompletion(t, wt, map[string]any{"definition": "```mermaid\nflowchart LR\n  review\n```\n\n## review\nReview\n"})
	if info.ParentSessionID != "first" || len(first.reqs) != 1 || len(second.reqs) != 0 {
		t.Fatalf("flow followed a different session after launch: %+v", info)
	}
}

const optimizer = "---\nname: polish\n---\n```mermaid\nflowchart LR\n  draft --> review\n  review -->|FAIL x3| draft\n  review -->|PASS| ship\n```\n\n## draft\nWrite it. Last review: {{review}}\n\n## review\nPASS or FAIL: {{draft}}\n\n## ship\nShip {{draft}}\n"

func TestFlowToolStatesLoopRounds(t *testing.T) {
	wt := NewFlowTool()
	wt.SetExecutor(&scriptedExecutor{})
	req, err := wt.PreparePermission(context.Background(), map[string]any{"definition": optimizer}, ".")
	if err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	want := "Run flow polish: 7 nodes, up to 7 subagent turns, max 4 parallel\ndraft/review: up to 3 rounds"
	if req.Description != want {
		t.Fatalf("description = %q,\nwant %q", req.Description, want)
	}
}

func TestFlowToolRetriesUntilReviewPasses(t *testing.T) {
	exec := &scriptedExecutor{outputs: map[string]string{
		"draft#1": "v1", "review#1": "FAIL",
		"draft#2": "v2", "review#2": "PASS",
		"ship": "shipped",
	}}
	wt := NewFlowTool()
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

func TestFlowToolFailsTheTaskWhenALoopRunsOut(t *testing.T) {
	// How a failed flow reaches the conversation: a failed task whose
	// error names the node, so the notification says what went wrong.
	exec := &scriptedExecutor{outputs: map[string]string{
		"draft#1": "v1", "review#1": "FAIL",
		"draft#2": "v2", "review#2": "FAIL",
		"draft#3": "v3", "review#3": "FAIL",
	}}
	wt := NewFlowTool()
	wt.SetExecutor(exec)
	params := map[string]any{"definition": optimizer}
	if _, err := wt.PreparePermission(context.Background(), params, "."); err != nil {
		t.Fatalf("PreparePermission: %v", err)
	}
	info := runToCompletion(t, wt, params)

	if info.Status != task.StatusFailed {
		t.Fatalf("status = %s, want failed\n%s", info.Status, info.Output)
	}
	if !strings.Contains(info.Error, "flow polish failed") || !strings.Contains(info.Error, "review#3") {
		t.Fatalf("task error = %q; it should name the flow and the node that failed", info.Error)
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	for _, r := range exec.reqs {
		if r.Description == "polish/ship" {
			t.Fatal("a draft that never passed review reached ship")
		}
	}
}

func TestFlowToolExecuteRefusesATypoedAgentWithoutApproval(t *testing.T) {
	// Execute is the only check that holds on the model's path: an allow rule
	// skips PreparePermission, and the approval prompt drops its error anyway.
	// So Execute must refuse a plan it would otherwise launch with the default
	// agent in place of the misspelled one.
	wt := NewFlowTool()
	wt.SetExecutor(&scriptedExecutor{unknown: "Explorr"})
	src := strings.Replace(definition, "agent: explorer", "agent: Explorr", 1)

	res := wt.Execute(context.Background(), map[string]any{"definition": src}, ".")
	if res.Success || !strings.Contains(res.Error, `no agent named "Explorr"`) {
		t.Fatalf("Execute = %+v; an unapproved call must get the same checks as an approved one", res)
	}
}

func TestFlowSchemaDoesNotRequireDelegation(t *testing.T) {
	description := NewFlowTool().Schema().Description
	if !strings.Contains(description, "For already-delegated work") || strings.Contains(description, "For one bounded job, or a one-off fan-out, use Agent instead") {
		t.Fatalf("flow schema redirects ordinary work to agents: %s", description)
	}
}
