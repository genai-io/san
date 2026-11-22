package agent

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestFastRecallIsNonBlockingAndLabelsPreviousQuestion(t *testing.T) {
	owner, stores := recallFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	driver := aitest.New(
		func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return aitest.Says(`{"selected_memories":["project/freeze.md"]}`)(ctx, req)
		},
		aitest.Says(`{"selected_memories":[]}`),
	)
	r := &fastRecall{ctx: t.Context(), stores: stores, recaller: Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}}
	defer r.cancel()
	first := core.UserMessage("提交 git 有什么要求？", nil)
	first.ID = "first"
	r.enqueue(first.ID, first.Text())
	original := []core.Message{first}
	initial := make(chan []core.Message, 1)
	go func() { initial <- r.prepare(original, false) }()
	select {
	case got := <-initial:
		if got[0].Text() != first.Text() {
			t.Fatal("first request used a result that was not ready")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first request waited for recall")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background recall did not start")
	}
	close(release)
	waitForFastRecall(t, r)
	if retry := r.prepare(original, true); len(retry) != 1 || retry[0].Text() != first.Text() {
		t.Fatal("a retry picked up a late result")
	}
	if got := r.prepare(original, false); len(got) != 2 || got[0].Text() != first.Text() || !strings.Contains(got[1].Text(), "no merges") || strings.Contains(got[1].Text(), "previous user question") {
		t.Fatalf("same-turn request got %+v", got)
	}
	second := core.UserMessage("那签名呢？", nil)
	second.ID = "second"
	r.enqueue(second.ID, second.Text())
	history := []core.Message{first, second}
	got := r.prepare(history, false)
	if len(got) != 3 || got[0].Text() != first.Text() || got[1].Text() != second.Text() {
		t.Fatalf("recall changed the request prefix: %+v", got)
	}
	if text := got[2].Text(); !strings.Contains(text, "previous user question") || !strings.Contains(text, "otherwise ignore") || !strings.Contains(text, "no merges") {
		t.Fatalf("previous recall was not tagged at the request tail: %+v", got)
	}
	if history[0].Text() != first.Text() || original[0].Text() != first.Text() {
		t.Fatal("recall rewrote history")
	}
	p := owner.Snapshot()
	for _, e := range p.Events() {
		if e.Type == session.EventMessageAppend {
			t.Fatal("background lookup wrote a history message")
		}
	}
	r.cancel()
	if got := r.prepare(history, false); len(got) != 2 || got[0].Text() != first.Text() {
		t.Fatalf("cancelled recall was reused: %+v", got)
	}
}

func waitForFastRecall(t *testing.T, r *fastRecall) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		ready := r.current != nil && len(r.current.notes) > 0
		r.mu.Unlock()
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("background recall did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFastRecallRefreshesReadyResultAfterExternalEditAndForget(t *testing.T) {
	owner, stores := recallFixture(t)
	path := filepath.Join(stores.Project, "freeze.md")
	// Preserve size and mtime (including time of day), so stat alone cannot detect the edit.
	stamp := time.Unix(1700000000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Says(`{"selected_memories":["project/freeze.md"]}`))
	r := &fastRecall{ctx: t.Context(), stores: stores, recaller: Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}}
	defer r.cancel()
	first := core.UserMessage("Can I merge?", nil)
	first.ID = "first"
	history := []core.Message{first}
	r.enqueue(first.ID, first.Text())
	r.prepare(history, false)
	waitForFastRecall(t, r)
	staged := r.prepare(history, false)
	if len(staged) != 2 || !strings.Contains(staged[1].Text(), "no merges") {
		t.Fatalf("expected ready recall in staged request: %+v", staged)
	}

	if err := os.WriteFile(path, []byte("---\ndescription: release freeze\n---\nyes merge"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if retry := r.prepare(history, true); len(retry) != 2 || retry[1].Text() != staged[1].Text() {
		t.Fatalf("edit changed the staged retry: %+v", retry)
	}
	updated := r.prepare(history, false)
	if len(updated) != 2 || !strings.Contains(updated[1].Text(), "yes merge") || strings.Contains(updated[1].Text(), "no merges") {
		t.Fatalf("fresh request did not use the current text: %+v", updated)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if retry := r.prepare(history, true); len(retry) != 2 || retry[1].Text() != updated[1].Text() {
		t.Fatalf("forget changed the staged retry: %+v", retry)
	}
	second := core.UserMessage("What about signing?", nil)
	second.ID = "second"
	r.enqueue(second.ID, second.Text())
	history = append(history, second)
	if got := r.prepare(history, false); len(got) != 2 || got[0].Text() != first.Text() || got[1].Text() != second.Text() {
		t.Fatalf("later request retained a forgotten note or changed history: %+v", got)
	}
}

func TestWaitRecallTimeoutDoesNotCancelTheTurn(t *testing.T) {
	owner, stores := recallFixture(t)
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		<-ctx.Done()
		return aitest.Says(`{"selected_memories":["project/freeze.md"]}`)(ctx, req)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	r := Recaller{Provider: compactRecordingProvider{driver}, Recording: owner, Timeout: 50 * time.Millisecond}
	msg := core.UserMessage("Can I merge?", nil)
	got, err := r.Prepare(ctx, stores, msg, nil)
	if err != nil || ctx.Err() != nil || got.Completion != nil || got.Message.Text() != msg.Text() {
		t.Fatalf("wait timeout did not preserve the main request: %+v, %v", got, err)
	}
}
