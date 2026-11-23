package agent

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestFastRecallIsNonBlockingAndLabelsPreviousQuestion(t *testing.T) {
	owner, store := recallFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	driver := aitest.New(
		func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return aitest.Says(`{"selected_memories":["freeze.md"]}`)(ctx, req)
		},
		aitest.Says(`{"selected_memories":["freeze.md","sign.md"]}`),
	)
	writeRecallNote(t, string(store), "sign.md", "sign commits")
	r := &fastRecall{ctx: t.Context(), store: store, recaller: Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}}
	defer r.cancel()
	first := core.UserMessage("提交 git 有什么要求？", nil)
	first.ID = "first"
	r.queueQuery(first.ID, first.Text())
	original := []core.Message{first}
	initial := make(chan []core.Message, 1)
	go func() { initial <- prepareFastMessages(r, original, false) }()
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
	if retry := prepareFastMessages(r, original, true); len(retry) != 1 || retry[0].Text() != first.Text() {
		t.Fatal("a retry picked up a late result")
	}
	second := core.UserMessage("那签名呢？", nil)
	second.ID = "second"
	r.queueQuery(second.ID, second.Text())
	history := []core.Message{first, second}
	got := prepareFastMessages(r, history, false)
	if len(got) != 3 || !reflect.DeepEqual(got[:2], history) || got[2].Role != ai.RoleUser || got[2].ID != "" {
		t.Fatalf("recall did not append a request-only user message: %+v", got)
	}
	if text := got[2].Text(); !strings.Contains(text, "previous question; use only if relevant") || !strings.Contains(text, "no merges") {
		t.Fatalf("late recall was not labeled: %+v", got)
	}
	waitForFastRecall(t, r)
	next := prepareFastMessages(r, history, false)
	if len(next) != 4 || !reflect.DeepEqual(next[:3], got) || !strings.Contains(next[3].Text(), "sign commits") ||
		strings.Contains(next[3].Text(), "freeze.md") {
		t.Fatalf("new result should extend the request without editing the earlier batch: %+v", next)
	}
	calls := driver.Sent()
	if len(calls) != 2 {
		t.Fatalf("expected two selector calls, got %d", len(calls))
	}
	if len(calls[0].Messages) != 3 || len(calls[1].Messages) != 3 || calls[0].System != calls[1].System ||
		!reflect.DeepEqual(calls[0].Messages[0], calls[1].Messages[0]) {
		t.Fatalf("coverage and question should not change the recall prefix: %+v", calls)
	}
	index := calls[1].Messages[0].Text()
	if !strings.Contains(index, "freeze.md") || !strings.Contains(index, "sign.md") ||
		calls[0].Messages[1].Text() != "Already in main context:\nNone." ||
		calls[1].Messages[1].Text() != "Already in main context:\n- freeze.md\n" ||
		calls[1].Messages[2].Text() != second.Text() {
		t.Fatalf("follow-up selector did not separate the index, active files, and question: %+v", calls[1].Messages)
	}
	if history[1].Text() != second.Text() || original[0].Text() != first.Text() {
		t.Fatal("recall rewrote history")
	}
	p := owner.Snapshot()
	for _, e := range p.Events() {
		if e.Type == session.EventMessageAppend {
			t.Fatal("background lookup wrote a history message")
		}
	}
	r.cancel()
	if got := prepareFastMessages(r, history, false); len(got) != 2 || got[0].Text() != first.Text() {
		t.Fatalf("cancelled recall was reused: %+v", got)
	}
}

func prepareFastMessages(r *fastRecall, messages []core.Message, retry bool) []core.Message {
	prepared, _ := r.prepareRequest(messages, retry)
	return prepared
}

func waitForFastRecall(t *testing.T, r *fastRecall) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		ready := r.latestLookup.hasMatches()
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
	owner, store := recallFixture(t)
	path := filepath.Join(string(store), "freeze.md")
	// Preserve size and mtime (including time of day), so stat alone cannot detect the edit.
	stamp := time.Unix(1700000000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Says(`{"selected_memories":["freeze.md"]}`))
	r := &fastRecall{ctx: t.Context(), store: store, recaller: Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}}
	defer r.cancel()
	first := core.UserMessage("Can I merge?", nil)
	first.ID = "first"
	history := []core.Message{first}
	r.queueQuery(first.ID, first.Text())
	prepareFastMessages(r, history, false)
	waitForFastRecall(t, r)
	staged := prepareFastMessages(r, history, false)
	if len(staged) != 2 || !reflect.DeepEqual(staged[0], first) || !strings.Contains(staged[1].Text(), "no merges") || strings.Contains(staged[1].Text(), "previous question") {
		t.Fatalf("expected ready recall in staged request: %+v", staged)
	}

	if err := os.WriteFile(path, []byte("---\ndescription: release freeze\n---\nyes merge"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if retry := prepareFastMessages(r, history, true); !reflect.DeepEqual(retry, staged) {
		t.Fatalf("edit changed the staged retry: %+v", retry)
	}
	updated := prepareFastMessages(r, history, false)
	if len(updated) != 2 || !reflect.DeepEqual(updated[0], first) || !strings.Contains(updated[1].Text(), "yes merge") || strings.Contains(updated[1].Text(), "no merges") {
		t.Fatalf("fresh request did not use the current text: %+v", updated)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if retry := prepareFastMessages(r, history, true); !reflect.DeepEqual(retry, updated) {
		t.Fatalf("forget changed the staged retry: %+v", retry)
	}
	second := core.UserMessage("What about signing?", nil)
	second.ID = "second"
	r.queueQuery(second.ID, second.Text())
	history = append(history, second)
	if got := prepareFastMessages(r, history, false); len(got) != 2 || got[0].Text() != first.Text() || got[1].Text() != second.Text() {
		t.Fatalf("later request retained a forgotten note or changed history: %+v", got)
	}
}

func TestFastRecallPreservesToolSuffixAndDropsCompactedContext(t *testing.T) {
	owner, store := recallFixture(t)
	const selection = `{"selected_memories":["freeze.md"]}`
	driver := aitest.New(aitest.Says(selection), aitest.Says(selection))
	r := &fastRecall{ctx: t.Context(), store: store, recaller: Recaller{Provider: runOnceProvider{driver}, Recording: owner}}
	defer r.cancel()
	user := core.UserMessage("Can I merge?", nil)
	user.ID = "user"
	user.Content = append(user.Content, ai.ImageBlock(ai.Image{MediaType: "image/png", Data: "eA=="}), ai.TextBlock("\n\n<system-reminder>Keep this.</system-reminder>"))
	call := core.AssistantMessage("Checking.", "", []core.ToolCall{{ID: "call", Name: "Read", Input: `{}`}})
	result := core.ToolResultMessage(core.ToolResult{ToolCallID: "call", ToolName: "Read", Content: ai.TextContent("done")})
	call.ID, result.ID = "assistant", "result"
	history := []core.Message{user, call, result}
	r.queueQuery(user.ID, "Can I merge?")
	prepareFastMessages(r, history[:1], false)
	waitForFastRecall(t, r)
	if got := prepareFastMessages(r, history[:2], false); !reflect.DeepEqual(got, history[:2]) {
		t.Fatalf("recall split an assistant call from its pending result: %+v", got)
	}
	request, notice := r.prepareRequest(history, false)
	got := ai.Repair(request)
	if len(got) != len(history)+1 || !reflect.DeepEqual(got[:len(history)], history) ||
		!strings.Contains(got[len(history)].Text(), "no merges") || strings.Contains(got[len(history)].Text(), "previous question") {
		t.Fatalf("same-turn recall should extend the intact tool request: %+v", got)
	}
	if want := (MemoryRecalled{AfterMessage: result.ID, NoteIDs: []string{"freeze.md"}}); notice == nil || !reflect.DeepEqual(notice(), want) {
		t.Fatal("recall notice should follow the tool result where the note was appended")
	}
	prepared, notice := r.prepareRequest(history[1:], false)
	if !reflect.DeepEqual(prepared, history[1:]) {
		t.Fatalf("recall changed a tool suffix without a user anchor: %+v", prepared)
	}
	if notice == nil || notice() != nil || len(r.lastUsedNotes) != 0 {
		t.Fatal("a response without recall should clear the previously shown notes")
	}
	fresh := core.UserMessage("Can I merge now?", nil)
	fresh.ID = "after-compaction"
	r.queueQuery(fresh.ID, fresh.Text())
	if got := prepareFastMessages(r, []core.Message{fresh}, false); !reflect.DeepEqual(got[0], fresh) {
		t.Fatalf("compacted memory was still attached: %+v", got)
	}
	waitForFastRecall(t, r)
	if driver.Calls() != 2 || !strings.Contains(driver.Last().Messages[0].Text(), "freeze.md") {
		t.Fatal("absent note was not offered again")
	}
}

func TestFastRecallUsesBoundedUserWindow(t *testing.T) {
	owner, store := recallFixture(t)
	driver := aitest.New(aitest.Says(`{"selected_memories":["freeze.md"]}`))
	r := &fastRecall{ctx: t.Context(), store: store, recaller: Recaller{Provider: runOnceProvider{driver}, Recording: owner}}
	defer r.cancel()
	first := core.UserMessage("Can I merge?", nil)
	first.ID = "first"
	history := []core.Message{first}
	r.queueQuery(first.ID, first.Text())
	prepareFastMessages(r, history, false)
	waitForFastRecall(t, r)
	prepareFastMessages(r, history, false)
	for i := 1; i < recallUserWindow; i++ {
		call := core.AssistantMessage("", "", []core.ToolCall{{ID: fmt.Sprintf("call-%d", i), Name: "Read", Input: `{}`}})
		call.ID = fmt.Sprintf("assistant-%d", i)
		tool := core.ToolResultMessage(core.ToolResult{ToolCallID: fmt.Sprintf("call-%d", i)})
		tool.ID = fmt.Sprintf("tool-%d", i)
		msg := core.UserMessage("Next question", nil)
		msg.ID = fmt.Sprintf("user-%d", i)
		history = append(history, call, tool, msg)
	}
	if got := prepareFastMessages(r, history, false); len(got) != len(history)+1 || !strings.Contains(got[1].Text(), "no merges") {
		t.Fatal("tool results counted toward the user-message limit")
	}
	last := core.UserMessage("Another question", nil)
	last.ID = "last"
	history = append(history, last)
	if got := prepareFastMessages(r, history, false); !reflect.DeepEqual(got, history) {
		t.Fatal("a note attached outside the recall window was still included")
	}
}

func TestFastRecallAppendedRequestReplaysWithoutChangingAcceptedInput(t *testing.T) {
	owner, store := recallFixture(t)
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if req.System == recallSystemPrompt {
			return aitest.Says(`{"selected_memories":["freeze.md"]}`)(ctx, req)
		}
		return aitest.Says("answer")(ctx, req)
	})
	policy := PermissionPolicy{Settings: setting.NewData(), Session: setting.NewSessionPermissions()}
	runtime := &Runtime{}
	if err := runtime.Start(BuildParams{Provider: runOnceProvider{driver}, ModelID: "fake", CWD: t.TempDir(), Recording: owner,
		AutoMemory: AutoMemoryConfig{Store: store}, PermissionRules: policy.Decide}, nil); err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, runtime)
	var notices []MemoryRecalled
	send := func(message core.Message) {
		t.Helper()
		if err := owner.AcceptInput(t.Context(), message, nil); err != nil {
			t.Fatal(err)
		}
		if err := runtime.SendInput(PendingInput{Message: message, FromUser: true}); err != nil {
			t.Fatal(err)
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case ev, ok := <-runtime.Outbox():
				if !ok {
					t.Fatalf("runtime stopped: %v", owner.Err())
				}
				if notice, ok := ev.(MemoryRecalled); ok {
					notices = append(notices, notice)
				}
				if _, ok := ev.(core.TurnEnded); ok {
					return
				}
			case <-timer.C:
				t.Fatal("turn did not complete")
			}
		}
	}
	first, second := core.UserMessage("Can I merge?", nil), core.UserMessage("And today?", nil)
	first.ID, second.ID = "first", "second"
	send(first)
	waitForFastRecall(t, runtime.run.recall)
	send(second)
	p := owner.Snapshot()
	if history := p.Messages(); len(history) != 4 || !reflect.DeepEqual(history[2], second) {
		t.Fatalf("native history contains the request-only addition: %+v", history)
	}
	var seq uint64
	for _, e := range p.Events() {
		if start, ok := e.Payload.(*session.InferenceStartPayload); ok && start.Purpose == session.PurposeTurn {
			seq = e.Seq
		}
	}
	replayed, err := owner.InferenceRequest(t.Context(), seq)
	if err != nil {
		t.Fatal(err)
	}
	sent := driver.Last()
	if len(sent.Messages) != 4 || !reflect.DeepEqual(sent.Messages[2], second) || sent.Messages[3].Role != ai.RoleUser ||
		!strings.Contains(sent.Messages[3].Text(), "no merges") || !reflect.DeepEqual(replayed.Messages, sent.Messages) {
		t.Fatalf("replay differs from the request with appended recall: got %+v, sent %+v", replayed.Messages, sent.Messages)
	}
	third := core.UserMessage("Is that still true?", nil)
	third.ID = "third"
	send(third)
	if want := []MemoryRecalled{{AfterMessage: second.ID, NoteIDs: []string{"freeze.md"}}}; !reflect.DeepEqual(notices, want) {
		t.Fatalf("expected one notice for the new attachment, got %+v", notices)
	}
	followUp := driver.Last().Messages
	if len(followUp) != len(sent.Messages)+2 || !reflect.DeepEqual(followUp[:len(sent.Messages)], sent.Messages) {
		t.Fatal("follow-up did not preserve the entire prior request as its prefix")
	}
}

func TestRuntimeRecallOffKeepsInputUnchanged(t *testing.T) {
	owner, store := recallFixture(t)
	if !store.HasNotes() {
		t.Fatal("fixture must contain a memory note")
	}
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if req.System == recallSystemPrompt {
			return aitest.Says(`{"selected_memories":["freeze.md"]}`)(ctx, req)
		}
		return aitest.Says("answer")(ctx, req)
	})
	policy := PermissionPolicy{Settings: setting.NewData(), Session: setting.NewSessionPermissions()}
	params := BuildParams{Provider: runOnceProvider{driver}, ModelID: "fake", CWD: t.TempDir(), Recording: owner,
		AutoMemory: AutoMemoryConfig{Store: store, Recall: setting.MemoryRecallConfig{Mode: setting.MemoryRecallOff}}, PermissionRules: policy.Decide}
	runtime := &Runtime{}
	if err := runtime.Start(params, nil); err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, runtime)
	if runtime.run.recall != nil {
		t.Fatal("Recall Off created a Fast selector")
	}
	message := core.UserMessage("Can I merge?", nil)
	message.ID = "recall-off"
	if err := owner.AcceptInput(t.Context(), message, nil); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SendInput(PendingInput{Message: message, FromUser: true}); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-runtime.Outbox():
			if !ok {
				t.Fatalf("runtime stopped: %v", owner.Err())
			}
			if _, ok := ev.(MemoryRecalled); ok {
				t.Fatal("Recall Off emitted a recall notice")
			}
			if _, ok := ev.(core.TurnEnded); ok {
				if driver.Calls() != 1 || driver.Last().System == recallSystemPrompt {
					t.Fatalf("expected only main inference; got %d calls", driver.Calls())
				}
				if sent := driver.Last().Messages; !reflect.DeepEqual(sent, []core.Message{message}) {
					t.Fatalf("main input changed: %+v", sent)
				}
				return
			}
		case <-timer.C:
			t.Fatal("turn did not complete")
		}
	}
}

func TestWaitRecallTimeoutDoesNotCancelTheTurn(t *testing.T) {
	owner, store := recallFixture(t)
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		<-ctx.Done()
		return aitest.Says(`{"selected_memories":["freeze.md"]}`)(ctx, req)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	r := Recaller{Provider: compactRecordingProvider{driver}, Recording: owner, Timeout: 50 * time.Millisecond}
	msg := core.UserMessage("Can I merge?", nil)
	got, err := r.Prepare(ctx, store, msg, nil)
	if err != nil || ctx.Err() != nil || got.Completion != nil || got.Message.Text() != msg.Text() {
		t.Fatalf("wait timeout did not preserve the main request: %+v, %v", got, err)
	}
}
