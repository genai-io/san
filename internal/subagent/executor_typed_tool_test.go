package subagent

import (
	"bufio"
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestTypedToolContinuationRetainsNativeEvidence(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	path := filepath.Join(cwd, "evidence.txt")
	if err := os.WriteFile(path, []byte("original evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := session.OpenStore(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	parent, err := store.Create(ctx, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	args, _ := json.Marshal(map[string]string{"file_path": path})
	var childID string
	checked := false
	first := func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		paths, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(parent.Path())), "*", "events.jsonl"))
		for _, path := range paths {
			if path != parent.Path() {
				childID = filepath.Base(filepath.Dir(path))
			}
		}
		file, err := os.Open(filepath.Join(filepath.Dir(filepath.Dir(parent.Path())), childID, "events.jsonl"))
		if err != nil {
			t.Error(err)
			return aitest.Fails(err)(ctx, req)
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		if !scanner.Scan() {
			t.Error("missing committed create")
		}
		var envelope struct {
			Batch struct {
				Records []struct {
					Data struct {
						Events []session.Event `json:"events"`
					} `json:"data"`
				} `json:"records"`
			} `json:"batch"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			t.Error(err)
		}
		var event session.Event
		if len(envelope.Batch.Records) > 0 && len(envelope.Batch.Records[0].Data.Events) > 0 {
			event = envelope.Batch.Records[0].Data.Events[0]
		}
		create, ok := event.Payload.(*session.SessionCreatePayload)
		if !ok || create.Parent == nil || create.Parent.Session != parent.ID() || create.Parent.CallID != "launch" {
			t.Errorf("wrong pre-dispatch identity: %+v", event)
		}
		checked = true
		return aitest.Replies(ai.Response{Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "read-original", Name: "Read", Input: string(args)})}, Usage: ai.Usage{Input: 11, Output: 7}})(ctx, req)
	}
	d := aitest.New(first, aitest.Replies(ai.Response{Content: ai.TextContent("continued"), Usage: ai.Usage{Input: 23, Output: 9}}))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	if err := e.SetTypedSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	a, err := e.RunBackground(tool.AgentExecRequest{Prompt: "original task", ParentToolCallID: "launch", MaxSteps: 1, OnIdentity: func(id toolresult.AgentIdentity) {
		if id.SessionID != "" {
			childID = id.SessionID
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !a.WaitForCompletion(5 * time.Second) {
		t.Fatal("hung")
	}
	if !checked {
		t.Fatal("provider callback not reached")
	}
	// Removing the file proves continuation consumes the committed result rather than rerunning Read.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	child, err := store.Open(ctx, a.GetStatus().AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := child.Snapshot()
	original := snapshot.ActiveMessages()
	child.Close()
	b, err := e.RunBackground(tool.AgentExecRequest{ResumeTaskID: a.GetID(), Prompt: "continue", ParentToolCallID: "later", MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !b.WaitForCompletion(5 * time.Second) {
		t.Fatal("hung")
	}
	if status := b.GetStatus(); status.Error != "" {
		t.Fatalf("continuation: %+v", status)
	}
	sent := d.Last().Messages
	if len(sent) <= len(original) {
		t.Fatalf("missing continuation original=%+v sent=%+v", original, sent)
	}
	for i, message := range original {
		if sent[i].ID != message.ID || sent[i].Text() != message.Text() {
			t.Fatalf("native message %d replaced", i)
		}
	}
	child, err = store.Open(ctx, a.GetStatus().AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	snapshot = child.Snapshot()
	tools, finishes := 0, 0
	for _, event := range snapshot.Events() {
		if start, ok := event.Payload.(*session.InferenceStartPayload); ok && start.Purpose != session.PurposeTurn {
			t.Fatalf("unexpected inference purpose %s", start.Purpose)
		}
		if payload, ok := event.Payload.(*session.ToolFinishPayload); ok {
			tools++
			if payload.CallID != "read-original" {
				t.Fatalf("unexpected replay %+v", payload)
			}
		}
		if payload, ok := event.Payload.(*session.InferenceFinishPayload); ok {
			finishes++
			if payload.Usage == nil {
				t.Fatal("missing usage")
			}
			want := 11
			if finishes == 2 {
				want = 23
			}
			if payload.Usage.Input != want {
				t.Fatalf("usage %+v", payload.Usage)
			}
		}
	}
	if tools != 1 || finishes != 2 || d.Calls() != 2 {
		t.Fatalf("tools=%d finishes=%d calls=%d", tools, finishes, d.Calls())
	}
}
