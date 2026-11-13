package subagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestAgentSessionLaunchProvenance(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "parent-command"}[linked], func(t *testing.T) {
			store, parent := continuationSessions(t, "owner")
			if !linked {
				parent = nil
			}
			d := aitest.New(aitest.Says("answer"))
			e := NewExecutor(continuationProvider{d}, t.TempDir(), "aitest", nil)
			if err := e.SetSessions(store, parent); err != nil {
				t.Fatal(err)
			}
			result, err := e.Run(context.Background(), tool.AgentExecRequest{Prompt: "work"})
			if err != nil {
				t.Fatal(err)
			}
			projection, err := store.Read(context.Background(), result.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			link := projection.Identity().Parent
			if !linked && link != nil {
				t.Fatalf("standalone fabricated parent: %+v", link)
			}
			if linked && (link == nil || link.Session != parent.ID() || link.CallID != "") {
				t.Fatalf("command fabricated call: %+v", link)
			}
			if result.SourceMessageSeq == 0 {
				t.Fatal("missing committed native answer")
			}
		})
	}
}

func TestChildHookDrainPrecedesWriterClose(t *testing.T) {
	store, parent := continuationSessions(t, "owner")
	e := NewExecutor(continuationProvider{aitest.New(aitest.Says("done"))}, t.TempDir(), "aitest", nil)
	if err := e.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	stopped, drained := false, false
	var childID string
	e.SetChildHooks(func(child *session.Session, _ llm.Provider, _ string) (hook.Handler, func(llm.Provider, string), func()) {
		childID = child.ID()
		handler := childStartHooks{dispatch: func(event hook.EventType) {
			if event == hook.SubagentStop {
				stopped = true
			}
		}}
		return handler, nil, func() {
			if !stopped {
				t.Error("child handler drained before stop dispatch")
			}
			if err := child.Append(context.Background(), session.Event{Payload: session.AnnotationAddPayload{Text: "stop hook drained"}}); err != nil {
				t.Error(err)
			}
			drained = true
		}
	})
	result, err := e.Run(context.Background(), tool.AgentExecRequest{Prompt: "work", ParentToolCallID: "launch"})
	if err != nil {
		t.Fatal(err)
	}
	if !drained || result.AgentID != childID {
		t.Fatal("wrong child hook lifetime")
	}
	projection, err := store.Read(context.Background(), childID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range projection.Events() {
		if payload, ok := event.Payload.(*session.AnnotationAddPayload); ok && payload.Text == "stop hook drained" {
			found = true
		}
	}
	if !found {
		t.Fatal("stop hook fact missing")
	}
	snapshot := parent.Snapshot()
	if len(snapshot.Events()) != 1 {
		t.Fatal("child hook wrote parent")
	}
	// A fresh writer claim is possible only after hook drain and owner close.
	child, err := store.Open(context.Background(), childID)
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
}

func TestChildPostToolRecordingFailureBlocksNextInference(t *testing.T) {
	store, parent := continuationSessions(t, "owner")
	cwd := t.TempDir()
	path := filepath.Join(cwd, "evidence.txt")
	if err := os.WriteFile(path, []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	d := aitest.New(aitest.Asks(ai.ToolCall{ID: "read", Name: "Read", Input: `{"file_path":"` + path + `"}`}), aitest.Says("must not run"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	if err := e.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("post tool hook recording failed")
	handler := childStartHooks{dispatch: func(hook.EventType) {}}
	e.SetChildHooks(func(*session.Session, llm.Provider, string) (hook.Handler, func(llm.Provider, string), func()) {
		return handler, nil, func() {}
	})
	e.SetPostTool(func(got hook.Handler, _ core.ToolResult, _ any) error {
		if got == nil {
			t.Error("missing captured child handler")
		}
		return sentinel
	})
	_, err := e.Run(context.Background(), tool.AgentExecRequest{Prompt: "read", ParentToolCallID: "launch"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("recording failure not returned: %v", err)
	}
	if d.Calls() != 1 {
		t.Fatalf("recording failure retried: %d calls", d.Calls())
	}
}
