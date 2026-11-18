package flow

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
)

func TestWriterCriticExampleRevisionScope(t *testing.T) {
	source, err := os.ReadFile("../../examples/flows/writer-critic.md")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range b.Nodes {
		if n.Executor != "subagent" {
			t.Fatalf("example must default to subagents: %s", n.ID)
		}
	}
	exec := &scriptedExecutor{outputs: map[string]string{
		"writer#1": "rough draft", "critic#1": "Verdict: REVISE\nadd examples", "decision#1": "REVISE",
		"writer#2": "revised article", "critic#2": "Verdict: PASS\ncomplete", "decision#2": "PASS", "result": "revised article",
	}}
	tasks := task.NewManager()
	s := NewService(tasks)
	s.store = NewStore(t.TempDir())
	s.SetExecutor(exec)
	id, err := s.start(b, map[string]string{"input": "write about everyday kindness"}, "example", 100)
	if err != nil {
		t.Fatal(err)
	}
	bg, _ := tasks.Get(id)
	waitFlowTask(t, bg)
	if info := bg.GetStatus(); info.Output != "revised article" {
		t.Fatalf("result: %+v", info)
	}
	prompts := map[string]string{}
	for _, req := range exec.reqs {
		prompts[req.Description] = req.Prompt
	}
	if len(prompts) != 7 {
		t.Fatalf("unexpected executed nodes: %v", prompts)
	}
	first, second := prompts["writer#1"], prompts["writer#2"]
	if strings.Contains(first, "rough draft\n") || strings.Contains(first, "add examples") {
		t.Fatal("first round inherited future outputs")
	}
	for _, want := range []string{"rough draft", "Verdict: REVISE\nadd examples", "write about everyday kindness"} {
		if !strings.Contains(second, want) {
			t.Fatalf("second writer lacks %q: %s", want, second)
		}
	}
	if !strings.Contains(prompts["critic#2"], "revised article") || strings.Contains(prompts["critic#2"], "rough draft") {
		t.Fatal("critic did not review current revision")
	}
}

// Keep completion synchronization explicit before inspecting scripted requests.
func waitFlowTask(t *testing.T, bg task.BackgroundTask) {
	t.Helper()
	if !bg.WaitForCompletion(5 * time.Second) {
		t.Fatal("flow did not finish")
	}
	if info := bg.GetStatus(); info.Error != "" {
		t.Fatalf("flow failed: %+v", info)
	}
}

type revisionMain struct{ scriptedMain }

func (m *revisionMain) Open(context.Context, MainRunInfo) (MainRunExecutor, error) { return m, nil }
func (m *revisionMain) Instructions() string {
	if m.calls == 1 {
		return "amendment A"
	}
	return "amendment A plus B"
}

func TestEarlierCriticResumeUsesItsWriterInstructions(t *testing.T) {
	b, err := Parse(mainLoopDefinition)
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewManager()
	tasks.SetSessionID("native-session")
	s := NewService(tasks)
	s.store = NewStore(t.TempDir())
	lease := &revisionMain{}
	s.SetMainExecutor(lease)
	exec := &scriptedExecutor{outputs: map[string]string{"critic#1": "revise", "decision#1": "REVISE", "critic#2": "pass", "decision#2": "PASS", "done": "answer"}}
	s.SetExecutor(exec)
	id, err := s.start(b, nil, "revisions", 100)
	if err != nil {
		t.Fatal(err)
	}
	bg, _ := tasks.Get(id)
	waitFlowTask(t, bg)
	records, err := s.Runs()
	if err != nil || len(records) != 1 {
		t.Fatalf("records: %v %v", records, err)
	}
	record := records[0]
	if record.Instructions != "amendment A plus B" || record.Nodes["writer#1"].Instructions != "amendment A" || record.Nodes["writer#2"].Instructions != "amendment A plus B" {
		t.Fatalf("revision snapshots lost: %+v", record)
	}
	exec2 := &scriptedExecutor{outputs: map[string]string{"critic#1": "pass", "decision#1": "PASS", "done": "answer"}}
	s.SetExecutor(exec2)
	id, err = s.ResumeRun(record.ID, "critic#1", 100)
	if err != nil {
		t.Fatal(err)
	}
	bg, _ = tasks.Get(id)
	waitFlowTask(t, bg)
	if lease.calls != 2 {
		t.Fatalf("reused writer replayed: %d", lease.calls)
	}
	for _, req := range exec2.reqs {
		if !strings.Contains(req.Prompt, "amendment A") || strings.Contains(req.Prompt, "plus B") {
			t.Fatalf("later amendments leaked into earlier review: %s", req.Prompt)
		}
	}
}

func TestMainResumeWithoutIdentityRequiresExplicitFrom(t *testing.T) {
	b, err := Parse(mainLoopDefinition)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []Status{StatusFailed, StatusRunning} {
		tasks := task.NewManager()
		tasks.SetSessionID("native-session")
		s := NewService(tasks)
		s.store = NewStore(t.TempDir())
		s.SetExecutor(&scriptedExecutor{})
		s.SetMainExecutor(&scriptedMain{})
		r := RunRecord{ID: "unknown-admission", Definition: b.Source, SessionID: "native-session", Status: RunFailed, Nodes: map[string]Checkpoint{"writer#1": {Outcome: Outcome{Status: status}}}}
		if err := s.store.Put(r); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ResumeRun(r.ID, "", 100); err == nil || !strings.Contains(err.Error(), "explicit from") {
			t.Fatalf("%s allowed implicit native replay: %v", status, err)
		}
	}
}

type amendmentMain struct {
	scriptedMain
	instructions string
}

func (m *amendmentMain) Instructions() string { return m.instructions }

func TestAcceptedInstructionsFrozenBeforeCheckpointAndCritic(t *testing.T) {
	b, err := Parse(mainLoopDefinition)
	if err != nil {
		t.Fatal(err)
	}
	lease := &amendmentMain{instructions: "accepted: include everyday examples"}
	exec := &scriptedExecutor{outputs: map[string]string{"critic#1": "review", "decision#1": "PASS", "done": "answer"}}
	tasks := task.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bg := tasks.CreateSchedulerTask(task.NewID(), "test", "test", ctx, cancel)
	persisted := ""
	runner := &nodeRunner{exec: exec, main: lease, task: bg, blueprint: b, onInstructions: func(_ *Node, snapshot string) error {
		persisted = snapshot
		// Future edits are queued, not accepted for the revision under review.
		lease.instructions = "future queued: change the topic"
		return nil
	}}
	res := Run(ctx, b, runner, Options{Route: mainRoute(ctx, b, lease), OnCheckpoint: func(n *Node, c Checkpoint) error {
		if n.ID == "writer#1" && c.Status == StatusSucceeded && persisted != "accepted: include everyday examples" {
			t.Error("success preceded durable amendment snapshot")
		}
		return nil
	}})
	if res.Failed(b) {
		t.Fatal(res.Failures(b))
	}
	for _, req := range exec.reqs {
		if !strings.Contains(req.Prompt, persisted) || strings.Contains(req.Prompt, "change the topic") {
			t.Fatalf("untruthful amendment snapshot: %s", req.Prompt)
		}
	}
}
