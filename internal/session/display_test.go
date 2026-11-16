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

func TestNativeDisplayLegacyRecapEcho(t *testing.T) {
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{ID: "recap-echo"})
	note := AnnotationAddPayload{Text: "/recap \n\nDone."}
	for range 2 {
		p := s.Snapshot()
		inference, err := s.StartInference(t.Context(), InferenceOptions{Purpose: PurposeRecap, Provider: "test", Model: ai.Model{ID: "model"}, Request: conversationRequest(&p, nil)})
		if err != nil {
			t.Fatal(err)
		}
		if err := inference.Finish(t.Context(), &ai.Response{}, nil, Event{Payload: note}); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(t.Context(), Event{Payload: note}); err != nil {
			t.Fatal(err)
		}
	}
	// Unproven unsourced pairs, even identical recap text, must remain visible.
	if err := s.Append(t.Context(), Event{Payload: note}, Event{Payload: note}); err != nil {
		t.Fatal(err)
	}
	path := store.eventsPath(s.ID())
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		p, err := store.Read(t.Context(), s.ID())
		if err != nil {
			t.Fatal(err)
		}
		original := eventCopy(p.data)
		if rows := store.ChatRows(p); len(rows) != 4 {
			t.Fatalf("expected two intentional recaps and two ambiguous notices: %+v", rows)
		}
		if !reflect.DeepEqual(original, p.data) {
			t.Fatal("display mutated projection")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("display changed durable events: %v", err)
	}
}

func TestLegacyRecapEchoRequiresExactProvenance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]Event)
	}{
		{"other purpose", func(e []Event) { e[0].Payload.(*InferenceStartPayload).Purpose = PurposeBtw }},
		{"failed finish", func(e []Event) { e[2].Payload.(*InferenceFinishPayload).Outcome = OutcomeFailure }},
		{"unsourced original", func(e []Event) { e[1].Source = nil }},
		{"sourced second notice", func(e []Event) { e[3].Source = e[1].Source }},
		{"different finish source", func(e []Event) { e[2].Source = &EventRef{Session: "s", Seq: 2} }},
		{"foreign source", func(e []Event) { e[1].Source.Session = "foreign"; e[2].Source.Session = "foreign" }},
		{"different text", func(e []Event) { e[3].Payload.(*AnnotationAddPayload).Text += " different" }},
		{"different agent", func(e []Event) { e[3].Payload.(*AnnotationAddPayload).ByAgent = true }},
		{"different anchor", func(e []Event) { e[3].Payload.(*AnnotationAddPayload).AfterMessage = "message" }},
		{"ordinary notices", func(e []Event) {
			e[1].Payload.(*AnnotationAddPayload).Text = "notice"
			e[3].Payload.(*AnnotationAddPayload).Text = "notice"
		}},
		{"intervening event", func(e []Event) { e[2].Payload = &AnnotationAddPayload{Text: "intervening"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []Event{
				{Seq: 1, Payload: &InferenceStartPayload{Purpose: PurposeRecap}},
				{Seq: 2, Source: &EventRef{Seq: 1}, Payload: &AnnotationAddPayload{Text: "/recap \n\nDone."}},
				{Seq: 3, Source: &EventRef{Seq: 1}, Payload: &InferenceFinishPayload{Outcome: OutcomeSuccess}},
				{Seq: 4, Payload: &AnnotationAddPayload{Text: "/recap \n\nDone."}},
			}
			if !legacyRecapEcho(events, 3) {
				t.Fatal("fixture must match before modification")
			}
			tc.change(events)
			if legacyRecapEcho(events, 3) {
				t.Fatal("ambiguous notice was classified as a recap echo")
			}
		})
	}
}

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

func TestNativeDisplayDetachesToolDetails(t *testing.T) {
	result := ai.ToolResult{ToolCallID: "call", Content: ai.TextContent("original")}
	p := Projection{data: projectionData{
		Messages: []ai.Message{core.ToolResultMessage(result)},
		Events: []Event{{Type: EventToolFinish, Payload: &ToolFinishPayload{
			CallID: "call", Details: json.RawMessage(`{"future":"metadata"}`),
		}}},
	}}
	before := eventCopy(p.data)
	rows := (&Store{}).ChatRows(p)
	raw, ok := rows[0].ToolDetails.(json.RawMessage)
	if !ok {
		t.Fatalf("unknown details = %T, want raw JSON", rows[0].ToolDetails)
	}
	raw[0] = ' '
	rows[0].ToolResult.Content[0].Text = "changed"
	if !reflect.DeepEqual(before, p.data) {
		t.Fatal("display rows share mutable values with the projection")
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
