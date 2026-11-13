package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/tests/integration/testutil"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func newTestStore(t *testing.T) *session.Store {
	t.Helper()
	return testutil.OpenSessionStore(t, t.TempDir())
}
func makeUserEntry(id, text string) core.Message {
	return ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(text)}
}
func makeAssistantEntry(id, text string) core.Message {
	return ai.Message{ID: id, Role: ai.RoleAssistant, Content: ai.TextContent(text)}
}
func importMessages(t *testing.T, store *session.Store, id string, messages ...ai.Message) {
	t.Helper()
	if err := testutil.ImportSession(store, session.CreateOptions{ID: id}, messages); err != nil {
		t.Fatal(err)
	}
}
func readSession(t *testing.T, store *session.Store, id string) session.Projection {
	t.Helper()
	p, err := store.Read(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func openSession(t *testing.T, store *session.Store, id string) *session.Session {
	t.Helper()
	owner, err := store.Open(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	return owner
}
func appendMessages(t *testing.T, owner *session.Session, messages ...ai.Message) {
	t.Helper()
	if err := testutil.AppendMessages(owner, messages); err != nil {
		t.Fatal(err)
	}
}
func appendState(t *testing.T, owner *session.Session, state session.StatePatch) {
	t.Helper()
	if err := owner.Append(t.Context(), session.Event{Payload: session.SessionUpdatePayload{State: &state}}); err != nil {
		t.Fatal(err)
	}
}
func listSessions(t *testing.T, store *session.Store) []session.ListItem {
	t.Helper()
	page, err := store.List(t.Context(), session.ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func TestSession_SaveAndLoad(t *testing.T) {
	store := newTestStore(t)
	config := &session.EventConfig{Provider: new("fake"), Model: new("fake-model")}
	if err := testutil.ImportSession(store, session.CreateOptions{ID: "test-1", Config: config}, []ai.Message{makeUserEntry("u1", "hello"), makeAssistantEntry("a1", "hi there")}); err != nil {
		t.Fatal(err)
	}
	owner := openSession(t, store, "test-1")
	appendState(t, owner, session.StatePatch{Title: session.SetField("Test Session")})
	loaded := readSession(t, store, "test-1")
	if *loaded.State().Title != "Test Session" || *loaded.Config().Provider != "fake" || len(loaded.Messages()) != 2 || loaded.Messages()[0].Text() != "hello" {
		t.Fatalf("loaded=%+v", loaded)
	}
}
func TestSession_List(t *testing.T) {
	store := newTestStore(t)
	for _, title := range []string{"First", "Second", "Third"} {
		importMessages(t, store, title)
		owner := openSession(t, store, title)
		appendState(t, owner, session.StatePatch{Title: session.SetField(title)})
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	list := listSessions(t, store)
	if len(list) != 3 || list[0].Title != "Third" {
		t.Fatalf("list=%+v", list)
	}
}
func TestSession_Delete(t *testing.T) {
	store := newTestStore(t)
	importMessages(t, store, "to-delete")
	if err := store.Delete(t.Context(), "to-delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(t.Context(), "to-delete"); err == nil {
		t.Fatal("deleted session readable")
	}
	if len(listSessions(t, store)) != 0 {
		t.Fatal("trashed session listed live")
	}
}
func TestSession_AppendBehavior(t *testing.T) {
	store := newTestStore(t)
	importMessages(t, store, "append-test", makeUserEntry("u1", "hello"))
	owner := openSession(t, store, "append-test")
	appendMessages(t, owner, makeAssistantEntry("a1", "hi there"), makeUserEntry("u2", "how are you?"))
	p := readSession(t, store, "append-test")
	if len(p.Messages()) != 3 || p.Messages()[2].Text() != "how are you?" {
		t.Fatalf("messages=%+v", p.Messages())
	}
}
func TestSession_MetadataUpdatesOnNewMessage(t *testing.T) {
	store := newTestStore(t)
	importMessages(t, store, "metadata-update-test", makeUserEntry("u1", "hello"))
	first := listSessions(t, store)[0]
	time.Sleep(10 * time.Millisecond)
	owner := openSession(t, store, "metadata-update-test")
	appendMessages(t, owner, makeAssistantEntry("a1", "hi there"))
	second := listSessions(t, store)[0]
	if first.Messages != 1 || second.Messages != 2 || second.LastActive <= first.LastActive {
		t.Fatalf("before=%+v after=%+v", first, second)
	}
}
func TestSession_MessageTypes_PersistRoundTrip(t *testing.T) {
	store := newTestStore(t)
	messages := []ai.Message{makeUserEntry("u1", "read this file"), {ID: "a1", Role: ai.RoleAssistant, Content: ai.Content{ai.TextBlock("I'll inspect it."), ai.ThinkingBlock("need to inspect the file", "sig-1"), ai.ReasoningBlock(ai.ReasoningItem{ID: "reason", EncryptedContent: "opaque"}), ai.ToolCallBlock(ai.ToolCall{ID: "tc-1", Name: "Read", Input: `{"file_path":"/tmp/test.txt"}`, Signature: []byte("signed")})}}, {ID: "u2", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "tc-1", ToolName: "Read", Content: ai.TextContent("file contents")})}}, makeAssistantEntry("a2", "done")}
	importMessages(t, store, "message-types-roundtrip", messages...)
	loaded := readSession(t, store, "message-types-roundtrip")
	if !reflect.DeepEqual(messages, loaded.Messages()) {
		t.Fatalf("native messages changed: %+v", loaded.Messages())
	}
}
func TestSession_PersistToolResult(t *testing.T) {
	store := newTestStore(t)
	id := "tool-overflow-test"
	call := "tc-abc123"
	content := strings.Repeat("x", 200_000)
	messages := []ai.Message{makeUserEntry("question", "run"), {ID: "call", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: call, Name: "Bash", Input: `{}`})}}, {ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: call, ToolName: "Bash", Content: ai.TextContent("preview\n\n[Full output persisted to blobs/tool-result/" + id + "/" + call + "]")})}}}
	importMessages(t, store, id, messages...)
	if err := store.PersistToolResult(id, call, content); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(store.ProjectDir(), "sessions", id, "blobs", "tool-result", call))
	if err != nil || string(data) != content {
		t.Fatalf("asset=%d err=%v", len(data), err)
	}
	p := readSession(t, store, id)
	rows := store.ChatRows(p)
	if rows[len(rows)-1].ToolResult.Text() != content {
		t.Fatal("preview not hydrated")
	}
	if p.Messages()[2].ToolResults()[0].Content.Text() == content {
		t.Fatal("native preview overwritten")
	}
}
func TestSession_JSONL_Integrity(t *testing.T) {
	store := newTestStore(t)
	importMessages(t, store, "jsonl-integrity-test", makeUserEntry("u1", "first message"), makeAssistantEntry("a1", "first response"), makeUserEntry("u2", "second message"), makeAssistantEntry("a2", "special chars: <>&\"'"))
	path := filepath.Join(store.ProjectDir(), "sessions", "jsonl-integrity-test", "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" {
			continue
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(line), &object); err != nil {
			t.Fatal(err)
		}
		lines++
	}
	batches := 0
	for _, err := range store.CommittedBatches(t.Context(), "jsonl-integrity-test") {
		if err != nil {
			t.Fatal(err)
		}
		batches++
	}
	if lines != batches || lines != 7 {
		t.Fatalf("lines=%d batches=%d", lines, batches)
	}
}
func TestSession_ContinueRestoresMessages(t *testing.T) {
	store := newTestStore(t)
	texts := []string{"hello", "hi there", "what is 2+2?", "4", "thanks"}
	var messages []ai.Message
	for i, text := range texts {
		message := makeUserEntry(fmt.Sprintf("id-%d", i), text)
		if i%2 == 1 {
			message.Role = ai.RoleAssistant
		}
		messages = append(messages, message)
	}
	importMessages(t, store, "continue-test", messages...)
	p := readSession(t, store, "continue-test")
	if !reflect.DeepEqual(messages, p.Messages()) {
		t.Fatalf("replay=%+v", p.Messages())
	}
}
func TestSession_SaveTwice_NoDuplication(t *testing.T) {
	store := newTestStore(t)
	messages := []ai.Message{makeUserEntry("m1", "hello"), makeAssistantEntry("m2", "hi")}
	importMessages(t, store, "save-twice", messages...)
	if err := testutil.ImportSession(store, session.CreateOptions{ID: "save-twice"}, messages); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	owner := openSession(t, store, "save-twice")
	if err := testutil.AppendMessages(owner, messages[:1]); err == nil {
		t.Fatal("duplicate message accepted")
	}
	p := readSession(t, store, "save-twice")
	if len(p.Messages()) != 2 {
		t.Fatal("duplicate persisted")
	}
	count := 0
	for _, event := range p.Events() {
		if event.Type == session.EventMessageAppend {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("message events=%d", count)
	}
}
func TestSession_FirstSaveAfterRestart_PicksUpDiskState(t *testing.T) {
	store := newTestStore(t)
	importMessages(t, store, "restart-group", makeUserEntry("m1", "hi"))
	owner := openSession(t, store, "restart-group")
	appendState(t, owner, session.StatePatch{GroupHint: session.SetField(session.GroupHint{GroupID: "urgent", MemberID: "main"})})
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	persisted := readSession(t, store, "restart-group")
	identity := persisted.Identity()
	restarted, err := session.OpenStore(context.Background(), identity.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	owner = openSession(t, restarted, "restart-group")
	appendState(t, owner, session.StatePatch{GroupHint: session.ClearField[session.GroupHint]()})
	cleared := readSession(t, restarted, "restart-group")
	if cleared.State().GroupHint != nil {
		t.Fatal("group clear lost on restart")
	}
}
func TestSession_ImagesSurviveRestartAndFork(t *testing.T) {
	store := newTestStore(t)
	message := ai.Message{ID: "image-message", Role: ai.RoleUser, Content: ai.Content{ai.TextBlock("before"), ai.ImageBlock(ai.Image{MediaType: "image/png", Data: "aGVsbG8="}), ai.TextBlock("after"), ai.TextBlock("[Image #1: /tmp/source.png]")}}
	importMessages(t, store, "images", message)
	dir := store.ImagesDir("images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.png"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity := readSession(t, store, "images")
	workspace := identity.Identity().Workspace
	restarted, err := session.OpenStore(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	loaded := readSession(t, restarted, "images")
	if !reflect.DeepEqual(loaded.Messages(), []ai.Message{message}) {
		t.Fatal("native image changed after restart")
	}
	rows := restarted.ChatRows(loaded)
	if rows[0].DisplayContent != "before[Image #1]after" || len(rows[0].Images) != 1 {
		t.Fatalf("image display=%+v", rows)
	}
	fork, err := restarted.Fork(t.Context(), "images", session.ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()
	forked := fork.Snapshot()
	if !reflect.DeepEqual(forked.Messages(), loaded.Messages()) {
		t.Fatal("fork changed native image")
	}
	data, err := os.ReadFile(filepath.Join(restarted.ImagesDir(fork.ID()), "source.png"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("fork image=%q %v", data, err)
	}
}

func TestSession_SaveClearedTasks_ClearsOnReload(t *testing.T) {
	store := newTestStore(t)
	importMessages(t, store, "clear-tasks", makeUserEntry("m1", "hi"))
	owner := openSession(t, store, "clear-tasks")
	appendState(t, owner, session.StatePatch{Todos: session.SetField([]todo.Item{{ID: "t1", Subject: "do thing", Status: "in_progress"}})})
	appendState(t, owner, session.StatePatch{Todos: session.SetField([]todo.Item{})})
	p := readSession(t, store, "clear-tasks")
	if p.State().Todos == nil || len(*p.State().Todos) != 0 {
		t.Fatal("stale tasks resurrected")
	}
}
