package session

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/san/internal/transcript"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestMessagesSurviveWriteAndResumeUnchanged(t *testing.T) {
	messages := []ai.Message{
		{ID: "user", Role: ai.RoleUser, Content: ai.Content{ai.TextBlock("before"), ai.ImageBlock(ai.Image{MediaType: "image/png", Data: "aGVsbG8="}), ai.TextBlock("after")}},
		{ID: "assistant", Role: ai.RoleAssistant, Content: ai.Content{ai.ThinkingBlock("thought", "signature"), ai.ReasoningBlock(ai.ReasoningItem{ID: "r", EncryptedContent: "opaque"}), ai.TextBlock("answer"), ai.ToolCallBlock(ai.ToolCall{ID: "call", Name: "Read", Input: "{}", Signature: []byte("signed")})}},
		{ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", ToolName: "Read", Content: ai.Content{ai.TextBlock("result"), ai.ImageBlock(ai.Image{MediaType: "image/png", Data: "aGVsbG8="})}})}},
	}
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "snapshot", true: "live"}[live], func(t *testing.T) {
			store, err := NewStoreWithDir(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if live {
				w := mustSession(t, store.transcriptStore, CreateOptions{ID: "s", AgentID: "main", Provider: "test", Model: "model", MaxTokens: 100})
				defer w.Close()
				for _, m := range messages {
					w.RecordAgentEvent(sdkagent.MessageAdded{Message: m})
				}
				if err := w.Err(); err != nil {
					t.Fatal(err)
				}
			} else if err := store.importHistory(&History{Metadata: Metadata{ID: "s"}, Messages: messages}); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Read("s")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(messages, loaded.Messages) {
				t.Fatalf("conversation changed during persistence: before=%+v after=%+v", messages, loaded.Messages)
			}
		})
	}
}

func TestSessionFailureKeepsAReplayablePrefix(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := mustSession(t, store.transcriptStore, CreateOptions{ID: "s", AgentID: "main", Provider: "test", Model: "model", MaxTokens: 100})
	defer w.Close()
	emit := func(id string) {
		w.RecordAgentEvent(sdkagent.MessageAdded{Message: ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(id)}})
	}
	emit("saved")
	path := filepath.Join(dir, "transcripts")
	held := filepath.Join(dir, "held")
	if err := os.Rename(path, held); err != nil {
		t.Fatal(err)
	}
	emit("failed")
	if w.Err() == nil {
		t.Fatal("persistence failure was hidden")
	}
	if err := os.Rename(held, path); err != nil {
		t.Fatal(err)
	}
	emit("later")
	loaded, err := store.Read("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].ID != "saved" {
		t.Fatalf("saved prefix was lost: %+v", loaded.Messages)
	}
}

func TestForkOwnsItsToolOutputAndPreservesHistoryTime(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PersistToolResult("source", "call", "FULL OUTPUT"); err != nil {
		t.Fatal(err)
	}
	message := ai.Message{ID: "m", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", Content: ai.TextContent("preview\n\n[Full output persisted to blobs/tool-result/source/call]")})}}
	if err := store.importHistory(&History{Metadata: Metadata{ID: "source", CreatedAt: time.Unix(100, 0)}, Messages: []ai.Message{message}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.transcriptStore.Load(context.Background(), "source")
	if err != nil {
		t.Fatal(err)
	}
	forkSession, fork, err := store.Fork("source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forkSession.Close() })
	if err := store.Delete("source"); err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Read(fork.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Messages[0].ToolResults()[0].Text() != "FULL OUTPUT" {
		t.Fatal("fork lost its full output after the source was deleted")
	}
	after, err := store.transcriptStore.Load(context.Background(), fork.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Messages[0].Time.Equal(after.Messages[0].Time) {
		t.Fatal("fork changed historical message time")
	}
}

func TestSessionRecordsActualModelAndMonotonicInferences(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := mustSession(t, store.transcriptStore, CreateOptions{ID: "s", AgentID: "main", Provider: "initial", Model: "initial", MaxTokens: 100})
	w.RecordAgentEvent(sdkagent.MessageStart{Turn: 1, Attempt: 1, Inference: &sdkagent.Inference{Client: ai.NewClientWithDriver(aitest.Always(aitest.Says("ok")), ai.Model{ID: "actual"}), Options: []ai.Option{ai.WithMaxTokens(42)}}})
	w.Close()
	w = mustSession(t, store.transcriptStore, CreateOptions{ID: "s", AgentID: "main", Provider: "initial", Model: "initial", MaxTokens: 100})
	defer w.Close()
	w.RecordAgentEvent(sdkagent.MessageStart{Turn: 1, Attempt: 1, Inference: &sdkagent.Inference{Client: ai.NewClientWithDriver(aitest.Always(aitest.Says("ok")), ai.Model{ID: "second"})}})
	records := readAllRecords(t, store.projectDir, "s")
	var requests []*transcript.InferenceRecord
	for _, r := range records {
		if r.Type == transcript.InferenceRequested {
			requests = append(requests, r.Inference)
		}
	}
	if len(requests) != 2 || requests[0].Model != "actual" || requests[0].MaxTokens != 42 || requests[1].Sequence != 2 || requests[1].Exchange != 2 {
		t.Fatalf("incorrect model/position: %+v", requests)
	}
}

func TestRestoreProjectsDisplayWithoutChangingTheConversation(t *testing.T) {
	msg := core.Message{ID: "m", Role: ai.RoleUser, Content: ai.TextContent("hello\n\n<system-reminder>hidden</system-reminder>")}
	rows := ChatRows([]core.Message{msg})
	if len(rows) != 1 || rows[0].DisplayContent != "hello" {
		t.Fatalf("display projection=%+v", rows)
	}
	if msg.Text() != "hello\n\n<system-reminder>hidden</system-reminder>" {
		t.Fatal("projection changed model history")
	}
}

func TestRepeatedResumeDoesNotCopyHistoryAndReplacementDoesNotResurrectIt(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	history := []ai.Message{{ID: "old", Role: ai.RoleUser, Content: ai.TextContent("old")}}
	w := mustSession(t, store.transcriptStore, CreateOptions{ID: "s", AgentID: "main", Provider: "test", Model: "model", MaxTokens: 100})
	defer w.Close()
	w.RecordAgentEvent(sdkagent.MessageAdded{Message: history[0]})
	before := len(readAllRecords(t, store.projectDir, "s"))
	for range 3 {
		w.RecordAgentEvent(sdkagent.MessagesReplaced{Messages: history})
	}
	if got := len(readAllRecords(t, store.projectDir, "s")); got != before {
		t.Fatalf("resume duplicated history: %d -> %d", before, got)
	}
	w.RecordAgentEvent(sdkagent.MessagesReplaced{})
	w.RecordAgentEvent(sdkagent.MessageAdded{Message: ai.Message{ID: "new", Role: ai.RoleUser, Content: ai.TextContent("new")}})
	loaded, err := store.Read("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].ID != "new" {
		t.Fatalf("old history resurrected: %+v", loaded.Messages)
	}
}

func TestForkPreservesToolPresentationAndNativeResult(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := mustSession(t, store.transcriptStore, CreateOptions{ID: "source", AgentID: "main", Provider: "test", Model: "model", MaxTokens: 100})
	w.RecordAgentEvent(sdkagent.ToolEnd{ID: "call", Result: sdkagent.Result{Details: toolresult.FileChangeDetails{Path: "a.go", UnifiedDiff: "diff"}}})
	result := core.ToolResultMessage(core.ToolResult{ToolCallID: "call", ToolName: "Edit", Content: ai.TextContent("edited")})
	result.ID = "result"
	w.RecordAgentEvent(sdkagent.MessageAdded{Message: result})
	w.Close()
	forkSession, fork, err := store.Fork("source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forkSession.Close() })
	rows := fork.ChatRows()
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	got, ok := rows[0].ToolDetails.(toolresult.FileChangeDetails)
	if !ok || got.UnifiedDiff != "diff" {
		t.Fatalf("details lost: %+v", rows[0].ToolDetails)
	}
	if !reflect.DeepEqual(fork.Messages[0], result) {
		t.Fatal("native result was changed by presentation data")
	}
}

func TestForkOwnsPathsUsedByTextOnlyImageInput(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourceDir := store.ImagesDir("source")
	if err := os.MkdirAll(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(sourceDir, "chart.png")
	if err := os.WriteFile(imagePath, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	msg := core.UserMessage("read "+imagePath, nil)
	msg.ID = "image"
	if err := store.importHistory(&History{Metadata: Metadata{ID: "source"}, Messages: []core.Message{msg}}); err != nil {
		t.Fatal(err)
	}
	forkSession, fork, err := store.Fork("source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forkSession.Close() })
	if err := store.Delete("source"); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(store.ImagesDir(fork.Metadata.ID), "chart.png")
	if fork.Messages[0].Text() != "read "+wantPath {
		t.Fatal("fork retained the source image path")
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatal("fork lost its image:", err)
	}
}

func TestHistoryImportCannotWriteIntoAnotherActiveSession(t *testing.T) {
	dir := t.TempDir()
	first, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := mustSession(t, first.transcriptStore, CreateOptions{ID: "s", AgentID: "main", Provider: "test", Model: "model", MaxTokens: 100})
	w.RecordAgentEvent(sdkagent.MessageAdded{Message: core.Message{ID: "saved", Role: ai.RoleUser, Content: ai.TextContent("saved")}})
	second, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.importHistory(&History{Metadata: Metadata{ID: "s"}, Messages: []core.Message{{ID: "other", Role: ai.RoleUser, Content: ai.TextContent("other")}}}); err == nil {
		t.Fatal("snapshot bypassed writer ownership")
	}
	got, err := first.Read("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ID != "saved" {
		t.Fatal("active history was replaced")
	}
}
