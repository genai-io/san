package flow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
)

const mainLoopDefinition = "```mermaid\nflowchart LR\n writer --> critic --> decision\n decision -->|REVISE x3| writer\n decision -->|PASS| done\n```\n## writer\nexecutor: main\n\nwrite {{decision}}\n## critic\nreview {{writer}}\n## decision\nexact verdict {{critic}}\n## done\nfinish {{decision}}\n"

type scriptedMain struct {
	calls, reviews int
	pending        int
	accepted       []bool
	prompts        []string
	opened         MainRunInfo
}

func (m *scriptedMain) Open(_ context.Context, info MainRunInfo) (MainRunExecutor, error) {
	m.opened = info
	return m, nil
}
func (m *scriptedMain) RunNode(_ context.Context, c Call) (string, error) {
	m.calls++
	m.prompts = append(m.prompts, c.Prompt)
	if c.OnExecution != nil {
		c.OnExecution(Execution{MessageID: "genuine-input", SessionID: "native-session"})
	}
	return "native draft", nil
}
func (m *scriptedMain) Review(_ context.Context, accepted bool) (bool, error) {
	m.reviews++
	m.accepted = append(m.accepted, accepted)
	return m.reviews <= m.pending, nil
}
func (m *scriptedMain) Instructions() string { return "accepted amendment" }
func (m *scriptedMain) Close(error)          {}

type mainLoopRunner struct {
	main       *scriptedMain
	verdict    string
	donePrompt string
}

func (r *mainLoopRunner) RunNode(ctx context.Context, c Call) (string, error) {
	if c.Node.Executor == "main" {
		return r.main.RunNode(ctx, c)
	}
	switch c.Node.Base {
	case "decision":
		return r.verdict, nil
	case "done":
		r.donePrompt = c.Prompt
		return c.Prompt, nil
	default:
		return "truthful critique", nil
	}
}

func TestMainLoopRouteAndRestore(t *testing.T) {
	for _, pending := range []int{0, 1, 3} {
		t.Run(string(rune('0'+pending)), func(t *testing.T) {
			b, err := Parse(mainLoopDefinition)
			if err != nil {
				t.Fatal(err)
			}
			main := &scriptedMain{pending: pending}
			runner := &mainLoopRunner{main: main, verdict: "PASS"}
			saved := map[string]Checkpoint{}
			var mu sync.Mutex
			res := Run(context.Background(), b, runner, Options{Route: mainRoute(context.Background(), b, main), OnCheckpoint: func(n *Node, c Checkpoint) error { mu.Lock(); defer mu.Unlock(); saved[n.ID] = c; return nil }})
			if res.Failed(b) != (pending == 3) {
				t.Fatalf("failures: %v", res.Failures(b))
			}
			if main.calls != min(pending+1, 3) || main.reviews != main.calls {
				t.Fatalf("calls=%d reviews=%d", main.calls, main.reviews)
			}
			c := saved["writer#1"]
			if c.Execution.TaskID != "" || c.Execution.MessageID != "genuine-input" || c.Execution.SessionID != "native-session" {
				t.Fatalf("identity: %+v", c.Execution)
			}
			if pending > 0 {
				if main.prompts[1] != "write PASS" {
					t.Fatalf("route leaked into prompt: %q", main.prompts[1])
				}
				c = saved["decision#1"]
				if c.Output != "PASS" || c.Route != "REVISE" {
					t.Fatalf("original output lost: %+v", c)
				}
			}
			if pending < 3 {
				before := main.calls
				restored := Run(context.Background(), b, runner, Options{Resume: saved, Route: func(*Node, string) (string, error) { return "", errors.New("must not review reused turn") }})
				if restored.Failed(b) || main.calls != before {
					t.Fatalf("restore replayed: %v", restored.Failures(b))
				}
				if runner.donePrompt != "finish PASS" {
					t.Fatal(runner.donePrompt)
				}
			} else if !strings.Contains(saved["decision#3"].Error, "explicitly extend or rerun") {
				t.Fatal(saved["decision#3"])
			}
		})
	}
}

func TestMainRetryVerdictReviewsAsRejected(t *testing.T) {
	b, err := Parse(mainLoopDefinition)
	if err != nil {
		t.Fatal(err)
	}
	m := &scriptedMain{}
	res := Run(context.Background(), b, &mainLoopRunner{main: m, verdict: "REVISE"}, Options{Route: mainRoute(context.Background(), b, m)})
	if !res.Failed(b) || m.calls != 3 || m.reviews != 3 {
		t.Fatalf("calls=%d reviews=%d failures=%v", m.calls, m.reviews, res.Failures(b))
	}
	for _, accepted := range m.accepted {
		if accepted {
			t.Fatal("retry accepted")
		}
	}
}

func TestMainMalformedVerdictDoesNotReview(t *testing.T) {
	b, err := Parse(mainLoopDefinition)
	if err != nil {
		t.Fatal(err)
	}
	m := &scriptedMain{pending: 1}
	res := Run(context.Background(), b, &mainLoopRunner{main: m, verdict: "PASS with notes"}, Options{Route: mainRoute(context.Background(), b, m)})
	if !res.Failed(b) || m.reviews != 0 {
		t.Fatalf("reviews=%d failures=%v", m.reviews, res.Failures(b))
	}
}

func TestMainServiceSinkIdentityAndSessionSafety(t *testing.T) {
	tasks := task.NewManager()
	tasks.SetSessionID("native-session")
	s := NewService(tasks)
	s.store = NewStore(t.TempDir())
	m := &scriptedMain{}
	s.SetMainExecutor(m)
	b, err := Parse("```mermaid\nflowchart LR\n answer\n```\n## answer\nexecutor: main\n\nwrite\n")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.start(b, nil, "main test", 100)
	if err != nil {
		t.Fatal(err)
	}
	bg, ok := tasks.Get(id)
	if !ok || !bg.WaitForCompletion(5*time.Second) {
		t.Fatal("main task incomplete")
	}
	info := bg.GetStatus()
	if !info.ResultDisplayed || info.Output != "native draft" {
		t.Fatalf("result: %+v", info)
	}
	records, err := s.Runs()
	if err != nil || len(records) != 1 {
		t.Fatalf("records: %v %v", records, err)
	}
	r := records[0]
	if r.Instructions != "accepted amendment" {
		t.Fatalf("amendments not persisted: %+v", r)
	}
	if r.SessionID != "native-session" || r.Nodes["answer"].Execution.MessageID != "genuine-input" {
		t.Fatalf("identity: %+v", r)
	}
	tasks.SetSessionID("different")
	if _, err := s.ResumeRun(r.ID, "", 100); err == nil {
		t.Fatal("cross-session resume allowed")
	}
	tasks.SetSessionID("native-session")
	r.Status = RunFailed
	c := r.Nodes["answer"]
	c.Status = StatusFailed
	r.Nodes["answer"] = c
	if err := s.store.Put(r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResumeRun(r.ID, "", 100); err == nil || !strings.Contains(err.Error(), "explicit from") {
		t.Fatalf("implicit replay: %v", err)
	}
	id, err = s.ResumeRun(r.ID, "answer", 100)
	if err != nil {
		t.Fatal(err)
	}
	bg, _ = tasks.Get(id)
	if !bg.WaitForCompletion(5 * time.Second) {
		t.Fatal("rerun incomplete")
	}
	if m.opened.Instructions != "accepted amendment" {
		t.Fatalf("lease lost persisted amendments: %+v", m.opened)
	}
	tasks.SetSessionID("")
	if _, err = s.start(b, nil, "no session", 100); err == nil {
		t.Fatal("empty session accepted")
	}
	tasks.SetSessionID("native-session")
	s.SetMainExecutor(nil)
	if _, err = s.start(b, nil, "no executor", 100); err == nil {
		t.Fatal("missing main executor accepted")
	}
}

func TestExecutorParsing(t *testing.T) {
	base := "```mermaid\nflowchart LR\n a --> b\n```\n## a\n%s\nwork\n## b\n%s\nwork\n"
	cases := []struct {
		a, b  string
		valid bool
	}{
		{"", "", true}, {"executor: subagent", "", true}, {"", "executor: main", true},
		{"executor: main", "", false}, {"executor: other", "", false},
		{"executor: main", "executor: main", false},
		{"", "executor: main\nmodel: x", false}, {"", "executor: main\nmode: default", false},
		{"", "executor: main\nagent: x", false}, {"", "executor: main\ncontinue_on_error: true", false},
	}
	for _, c := range cases {
		source := strings.Replace(base, "%s", c.a, 1)
		source = strings.Replace(source, "%s", c.b, 1)
		b, err := Parse(source)
		if (err == nil) != c.valid {
			t.Fatalf("%q %q: %v", c.a, c.b, err)
		}
		if err == nil && c.a == "" && b.Nodes[0].Executor != "subagent" {
			t.Fatal("default changed")
		}
	}
}
