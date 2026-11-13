package session

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestNativeDisplayImagesSignaturesAndAnnotations(t *testing.T) {
	messages := []ai.Message{
		{ID: "user", Role: ai.RoleUser, Content: ai.Content{{Type: ai.BlockText, Text: "before"}, {Type: ai.BlockImage}, {Type: ai.BlockText, Text: "after"}, {Type: ai.BlockText, Text: "[Image #1: /tmp/source.png]"}}},
		{ID: "assistant", Role: ai.RoleAssistant, Content: ai.Content{ai.ThinkingBlock("thought", "signed"), ai.ReasoningBlock(ai.ReasoningItem{ID: "reason", EncryptedContent: "opaque"}), ai.TextBlock("answer")}},
	}
	before := eventCopy(messages)
	p := Projection{data: projectionData{Messages: messages, Events: []Event{
		{Type: EventAnnotationAdd, Payload: &AnnotationAddPayload{Text: "first"}},
		{Type: EventMessageAppend, Payload: &MessageAppendPayload{Messages: messages[:1]}},
		{Type: EventAnnotationAdd, Payload: &AnnotationAddPayload{Text: "own position"}},
		{Type: EventMessageAppend, Payload: &MessageAppendPayload{Messages: messages[1:]}},
		{Type: EventAnnotationAdd, Payload: &AnnotationAddPayload{Text: "anchored", AfterMessage: "user", ByAgent: true}},
	}}}
	rows := (&Store{}).ChatRows(p)
	if len(rows) != 5 || rows[0].Content != "first" || rows[1].DisplayContent != "before[Image #1]after" || rows[2].Content != "own position" || rows[3].Content != "anchored" || !rows[3].AgentNotice {
		t.Fatalf("rows=%+v", rows)
	}
	if rows[4].ThinkingSignature != "signed" || len(rows[4].Reasoning) != 1 || rows[4].Reasoning[0].EncryptedContent != "opaque" {
		t.Fatalf("native provider details lost: %+v", rows[4])
	}
	if !reflect.DeepEqual(messages, before) {
		t.Fatal("display changed native messages")
	}
}

func TestNativeDisplayPersistedHistoryAndChild(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	parent := createTyped(t, store, CreateOptions{ID: "parent"})
	if err := parent.Append(ctx,
		Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("old", ai.RoleUser, "old text")}}},
		Event{Payload: AnnotationAddPayload{Text: "old note"}},
		Event{Payload: ContextCompactPayload{Through: 2, Summary: eventText("summary", ai.RoleUser, "summary text")}},
		Event{Payload: AnnotationAddPayload{Text: "anchored note", AfterMessage: "old"}},
	); err != nil {
		t.Fatal(err)
	}
	child := createTyped(t, store, CreateOptions{ID: "child", Parent: &ParentLink{Session: "parent"}})
	if err := child.Append(ctx, Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("child-message", ai.RoleUser, "child text")}}}); err != nil {
		t.Fatal(err)
	}
	projection, err := store.Read(ctx, "parent")
	if err != nil {
		t.Fatal(err)
	}
	rows := store.ChatRows(projection)
	if len(rows) != 4 || rows[0].Content != "old text" || rows[1].Content != "old note" || rows[2].Content != "anchored note" || rows[3].Content != "summary text" {
		t.Fatalf("history=%+v", rows)
	}
	if len(projection.ActiveMessages()) != 1 {
		t.Fatal("display changed active context")
	}
	childProjection, err := store.Read(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	if childRows := store.ChatRows(childProjection); len(childRows) != 1 || childRows[0].Content != "child text" {
		t.Fatalf("child=%+v", childRows)
	}
	if err := store.PersistToolResult("parent", "call", "full"); err != nil {
		t.Fatal(err)
	}
	fork, err := store.Fork(ctx, "parent", ForkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()
	data, err := os.ReadFile(filepath.Join(store.toolResultsDir(fork.ID()), "call"))
	if err != nil || string(data) != "full" {
		t.Fatalf("fork asset=%q %v", data, err)
	}
}

func TestNativeDisplayPreviewDetailsAndWriterFreeRead(t *testing.T) {
	ctx := context.Background()
	store, err := openStoreIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opened := createTyped(t, store, CreateOptions{ID: "display"})
	defer opened.Close()
	if err := store.PersistToolResult("display", "call", "complete output"); err != nil {
		t.Fatal(err)
	}
	preview := "preview\n\n[Full output persisted to blobs/tool-result/display/call]"
	result := ai.ToolResult{ToolCallID: "call", ToolName: "Bash", Content: ai.TextContent(preview)}
	msg := core.ToolResultMessage(result)
	raw, _ := json.Marshal(toolresult.BashDetails{LineCount: 17, Tail: "tail"})
	p := Projection{data: projectionData{SessionID: "display", Messages: []ai.Message{msg}, Events: []Event{{Type: EventToolFinish, Payload: &ToolFinishPayload{CallID: "call", Details: raw}}}}}
	before := eventCopy(p.data)
	rows := store.ChatRows(p)
	if rows[0].ToolResult.Content[0].Text != "complete output" {
		t.Fatalf("result=%+v", rows)
	}
	if details, ok := rows[0].ToolDetails.(toolresult.BashDetails); !ok || details.LineCount != 17 {
		t.Fatalf("details=%+v", rows[0].ToolDetails)
	}
	if !reflect.DeepEqual(before, p.data) {
		t.Fatal("display changed preview")
	}
	path := store.eventsPath("display")
	disk, _ := os.ReadFile(path)
	if _, err := store.Read(ctx, "display"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(disk, after) {
		t.Fatal("read changed persisted events")
	}
	if store.ImagesDir("display") != filepath.Join(store.sessionDir("display"), "blobs", "images") || store.ImagesDir("../escape") != "" {
		t.Fatal("image layout or validation")
	}
}
