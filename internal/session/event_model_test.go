package session

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

type eventFixture struct {
	p       *Projection
	batches [][]Event
	ctx     EventValidationContext
}

func (f *eventFixture) add(t *testing.T, payload EventPayload, source string, outcome EventOutcome) Event {
	t.Helper()
	e := f.next(payload, source, outcome)
	f.batch(t, e)
	return e
}
func (f *eventFixture) next(payload EventPayload, source string, outcome EventOutcome) Event {
	seq := uint64(1)
	if f.p != nil {
		seq = uint64(len(f.p.Events()) + 1)
	}
	return Event{ID: fmt.Sprintf("event-%d", seq), SessionID: "session", Seq: seq, Type: payload.eventType(), SchemaVersion: 1, CreatedAt: 123, SourceID: source, Outcome: outcome, Payload: payload}
}
func (f *eventFixture) batch(t *testing.T, events ...Event) {
	t.Helper()
	p, err := ApplyEventBatch(f.p, events, f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.p = p
	f.batches = append(f.batches, events)
}
func newEventFixture(t *testing.T) *eventFixture {
	f := &eventFixture{ctx: EventValidationContext{ForeignEvents: map[string]ForeignEventEvidence{"child-result": {SessionID: "child", Type: EventMessageAppend, MessageIDs: []string{"original"}}}}}
	f.add(t, SessionCreatePayload{ProjectID: "project", Workspace: "/work", InitialCWD: "/work/sub", AgentID: "agent"}, "", "")
	return f
}
func eventText(id string, role ai.Role, text string) ai.Message {
	return ai.Message{ID: id, Role: role, Content: ai.TextContent(text)}
}
func fullEventFixture(t *testing.T) *eventFixture {
	f := newEventFixture(t)
	f.add(t, InstructionUpdatePayload{Upsert: []InstructionSection{{Name: "base", Text: "Be helpful"}}}, "", "")
	f.add(t, ToolsetUpdatePayload{Upsert: []ai.Schema{{Name: "Read", Description: "read"}}}, "", "")
	f.add(t, SkillUpdatePayload{Changes: []SkillActivation{{Name: "review", Active: true}}}, "", "")
	f.add(t, ConfigUpdatePayload{Patch: ConfigPatch{Model: SetField("model")}}, "", "")
	f.add(t, StateUpdatePayload{Patch: StatePatch{Title: SetField("title"), Pinned: SetField(false)}}, "", "")
	input := f.add(t, MessageAppendPayload{Messages: []ai.Message{eventText("user", ai.RoleUser, "question")}}, "", "")
	instructions, tools := f.p.CatalogueDigests()
	provider, model := "provider", "model"
	start := f.add(t, InferenceStartPayload{TurnID: "turn", InputMessageIDs: []string{"user"}, RequestConfig: EventConfig{Provider: &provider, Model: &model}, InstructionDigest: instructions, ToolsetDigest: tools}, "", "")
	call := ai.Message{ID: "call-message", Role: ai.RoleAssistant, Content: ai.Content{ai.ThinkingBlock("thought", "signature"), ai.ReasoningBlock(ai.ReasoningItem{ID: "reason", EncryptedContent: "opaque"}), ai.ToolCallBlock(ai.ToolCall{ID: "call", Name: "Read", Input: `{"path":"file"}`, Signature: []byte("signed")})}}
	invocation := f.add(t, MessageAppendPayload{Messages: []ai.Message{call}}, start.ID, "")
	tool := f.add(t, ToolStartPayload{CallID: "call", InputRef: InputRef{EventID: invocation.ID, MessageID: call.ID, CallID: "call"}}, "", "")
	permission := f.add(t, PermissionRequestPayload{OperationRef: tool.ID, Scope: PermissionScope{Resources: []string{"file"}}}, "", "")
	f.add(t, PermissionDecidePayload{Decision: "allow", Scope: PermissionScope{Resources: []string{"file"}}}, permission.ID, "")
	result := ai.Message{ID: "tool-result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", ToolName: "Read", Content: ai.Content{ai.TextBlock("before"), ai.ImageBlock(ai.Image{MediaType: "image/png", Data: "aGVsbG8="}), ai.TextBlock("after")}})}}
	f.add(t, MessageAppendPayload{Messages: []ai.Message{result}}, tool.ID, "")
	f.add(t, ToolFinishPayload{CallID: "call"}, tool.ID, OutcomeSuccess)
	delegation := f.add(t, DelegationStartPayload{ChildSessionID: "child", InputRef: InputRef{EventID: input.ID, MessageID: "user"}}, "", "")
	received := f.add(t, MessageAppendPayload{Messages: []ai.Message{eventText("received", ai.RoleAssistant, "child output")}, SourceMessageIDs: []string{"original"}}, "child-result", "")
	f.add(t, DelegationFinishPayload{ResultEventID: received.ID}, delegation.ID, OutcomeSuccess)
	f.add(t, HookFinishPayload{HookName: "audit", Trigger: "completion", EffectRefs: []string{received.ID}}, "", OutcomeSuccess)
	f.add(t, AnnotationAddPayload{Text: "note", Anchor: &AnnotationAnchor{MessageID: "user"}}, "", "")
	// Finish before apply is valid: the expected source is the pre-batch tail.
	tail := uint64(len(f.p.Events()))
	finish := f.next(InferenceFinishPayload{}, start.ID, OutcomeSuccess)
	compact := Event{ID: "compact", SessionID: "session", Seq: finish.Seq + 1, SchemaVersion: 1, Type: EventCompactionApply, SourceID: start.ID, Payload: CompactionApplyPayload{SourceSeq: tail, Summary: eventText("summary", ai.RoleAssistant, "summary"), RetainedMessageIDs: []string{"call-message", "tool-result", "received"}}}
	f.batch(t, finish, compact)
	return f
}
func TestEventFullReplayAndNativeRoundTrip(t *testing.T) {
	f := fullEventFixture(t)
	types := map[EventType]bool{}
	for _, e := range f.p.Events() {
		types[e.Type] = true
	}
	if len(types) != 18 {
		t.Fatalf("got %d types", len(types))
	}
	wire, err := json.Marshal(f.batches)
	if err != nil {
		t.Fatal(err)
	}
	var batches [][]Event
	if err = json.Unmarshal(wire, &batches); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		p, err := ReplayEventBatches(batches, f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p, f.p) {
			t.Fatal("replay changed projection")
		}
	}
	messages := f.p.Messages()
	if len(messages) != 5 || len(f.p.ActiveMessages()) != 4 || messages[0].ID != "user" {
		t.Fatal("compaction discarded history")
	}
	call := messages[1]
	if call.Content[0].Signature != "signature" || call.Content[1].Reasoning.EncryptedContent != "opaque" || string(call.Content[2].ToolCall.Signature) != "signed" {
		t.Fatal("native signatures lost")
	}
	blocks := messages[2].Content[0].ToolResult.Content
	if len(blocks) != 3 || blocks[1].Image.Data != "aGVsbG8=" {
		t.Fatal("native images/order lost")
	}
	if f.p.State().Title == nil || *f.p.State().Title != "title" || *f.p.Config().Model != "model" {
		t.Fatal("compaction reset state/config")
	}
}
func TestEventBatchValidationIsAllOrNone(t *testing.T) {
	f := newEventFixture(t)
	before := f.p.Events()
	one := f.next(StateUpdatePayload{Patch: StatePatch{Title: SetField("new")}}, "", "")
	two := one
	two.ID = "bad"
	two.Seq++
	two.SessionID = "wrong"
	got, err := ApplyEventBatch(f.p, []Event{one, two}, f.ctx)
	if err == nil || got != nil {
		t.Fatal("published invalid batch")
	}
	if !reflect.DeepEqual(before, f.p.Events()) || f.p.State().Title != nil {
		t.Fatal("mutated base")
	}
}
func TestEventMalformedValidation(t *testing.T) {
	tests := map[string]func(*Event){
		"sequence": func(e *Event) { e.Seq++ }, "session": func(e *Event) { e.SessionID = "other" }, "schema": func(e *Event) { e.SchemaVersion = 2 }, "id": func(e *Event) { e.ID = "" }, "source": func(e *Event) { e.SourceID = "missing" }, "outcome": func(e *Event) { e.Outcome = OutcomeSuccess }, "type": func(e *Event) { e.Type = EventToolStart },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			f := newEventFixture(t)
			e := f.next(AnnotationAddPayload{Text: "hello"}, "", "")
			change(&e)
			if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
				t.Fatal("accepted malformed event")
			}
		})
	}
	f := fullEventFixture(t)
	for name, payload := range map[string]EventPayload{
		"duplicate message": MessageAppendPayload{Messages: []ai.Message{eventText("user", ai.RoleUser, "duplicate")}},
		"bad anchor":        AnnotationAddPayload{Text: "note", Anchor: &AnnotationAnchor{Seq: 999}},
		"empty patch":       StateUpdatePayload{}, "overlap": InstructionUpdatePayload{Upsert: []InstructionSection{{Name: "x"}}, Remove: []string{"x"}},
		"stale compaction": CompactionApplyPayload{SourceSeq: 1, Summary: eventText("s2", ai.RoleUser, "summary")},
	} {
		t.Run(name, func(t *testing.T) {
			e := f.next(payload, "", "")
			if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
				t.Fatal("accepted malformed payload")
			}
		})
	}
	var inference string
	for _, e := range f.p.Events() {
		if e.Type == EventInferenceStart {
			inference = e.ID
		}
	}
	e := f.next(InferenceFinishPayload{}, inference, OutcomeSuccess)
	if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
		t.Fatal("duplicate finish accepted")
	}
	e.SourceID = "child-result"
	if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
		t.Fatal("foreign fake start accepted")
	}
}
func TestEventPatchTriStateAndStrictCodec(t *testing.T) {
	f := newEventFixture(t)
	f.add(t, StateUpdatePayload{Patch: StatePatch{Title: SetField(""), Pinned: SetField(false), ArchivedAt: SetField(int64(0))}}, "", "")
	s := f.p.State()
	if s.Title == nil || *s.Title != "" || s.Pinned == nil || *s.Pinned || s.ArchivedAt == nil || *s.ArchivedAt != 0 {
		t.Fatal("zero sets lost")
	}
	f.add(t, StateUpdatePayload{Patch: StatePatch{Title: ClearField[string]()}}, "", "")
	s = f.p.State()
	if s.Title != nil || s.Pinned == nil || s.ArchivedAt == nil {
		t.Fatal("absent/clear confused")
	}
	e := f.next(ConfigUpdatePayload{Patch: ConfigPatch{Model: SetField("")}}, "", "")
	b, _ := json.Marshal(e)
	for _, bad := range []string{strings.Replace(string(b), `"model":""`, `"secret":"password"`, 1), strings.Replace(string(b), `"patch":{`, `"patch":{"title":"bad",`, 1), strings.Replace(string(b), `"source_id"`, `"source_event_id"`, 1) + ` {}`} {
		var got Event
		if err := json.Unmarshal([]byte(bad), &got); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	f.batch(t, e)
	if f.p.Config().Model == nil || *f.p.Config().Model != "" {
		t.Fatal("empty model set lost")
	}
	f.add(t, ConfigUpdatePayload{Patch: ConfigPatch{Model: ClearField[string]()}}, "", "")
	if f.p.Config().Model != nil {
		t.Fatal("model clear lost")
	}
}
func TestEventDeepCopyIsolationAndRetry(t *testing.T) {
	f := fullEventFixture(t)
	original := f.p.Events()
	events := f.p.Events()
	events[0].Payload.(*SessionCreatePayload).ProjectID = "mutated"
	messages := f.p.Messages()
	messages[1].Content[2].ToolCall.Signature[0] = 'X'
	messages[2].Content[0].ToolResult.Content[1].Image.Data = "changed"
	state := f.p.State()
	*state.Title = "changed"
	config := f.p.Config()
	*config.Model = "changed"
	if !reflect.DeepEqual(original, f.p.Events()) {
		t.Fatal("accessor mutation escaped")
	}
	// Even stale compaction retry is recognized before checking its source tail.
	last := f.batches[len(f.batches)-1]
	p, err := ApplyEventBatch(f.p, last, f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, f.p) {
		t.Fatal("retry changed projection")
	}
	input := f.next(MessageAppendPayload{Messages: []ai.Message{eventText("fresh", ai.RoleUser, "text")}}, "", "")
	p, err = ApplyEventBatch(f.p, []Event{input}, f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	input.Payload.(MessageAppendPayload).Messages[0].Content[0].Text = "mutated"
	if p.Messages()[len(p.Messages())-1].Content[0].Text != "text" {
		t.Fatal("input mutation escaped")
	}
	conflict := original[0]
	conflict.CreatedAt++
	if _, err = ApplyEventBatch(f.p, []Event{conflict}, f.ctx); err == nil {
		t.Fatal("ID conflict accepted")
	}
}
func TestEventNativeSchemaNumberFidelity(t *testing.T) {
	f := newEventFixture(t)
	const exact int64 = 9007199254740993
	schema := ai.Schema{Name: "Exact", Definition: map[string]any{"const": exact, "enum": []any{exact}}}
	e := f.next(ToolsetUpdatePayload{Upsert: []ai.Schema{schema}}, "", "")
	wire, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err = json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	roundtrip, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(wire) != string(roundtrip) {
		t.Fatalf("native numeric wire changed: %s -> %s", wire, roundtrip)
	}
	f.batch(t, e)
	_, digest := f.p.CatalogueDigests()
	copy := f.p.Tools()
	number := copy[0].Definition.(map[string]any)["const"].(json.Number)
	if number.String() != "9007199254740993" {
		t.Fatal("schema number rounded")
	}
	copy[0].Definition.(map[string]any)["const"] = json.Number("1")
	retried, err := ApplyEventBatch(f.p, []Event{e}, f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, retriedDigest := retried.CatalogueDigests()
	if digest != retriedDigest {
		t.Fatal("copy/retry changed digest")
	}
	f.add(t, AnnotationAddPayload{Text: "clone state"}, "", "")
	_, after := f.p.CatalogueDigests()
	if after != digest {
		t.Fatal("batch clone changed numeric digest")
	}
}

func TestEventCompactionRetainedSequenceAndPairing(t *testing.T) {
	for name, ids := range map[string][]string{"reordered": {"tool-result", "call-message"}, "duplicate": {"received", "received"}, "historical": {"user"}, "orphan result": {"tool-result"}, "orphan call": {"call-message"}, "missing": {"missing"}} {
		t.Run(name, func(t *testing.T) {
			f := fullEventFixture(t)
			e := f.next(CompactionApplyPayload{SourceSeq: uint64(len(f.p.Events())), Summary: eventText("second-summary", ai.RoleAssistant, "summary"), RetainedMessageIDs: ids}, "", "")
			if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
				t.Fatal("accepted invalid retained sequence")
			}
		})
	}
	f := fullEventFixture(t)
	f.add(t, CompactionApplyPayload{SourceSeq: uint64(len(f.p.Events())), Summary: eventText("second-summary", ai.RoleAssistant, "summary"), RetainedMessageIDs: []string{"summary", "received"}}, "", "")
	if len(f.p.Messages()) != 6 {
		t.Fatal("consecutive compaction lost history")
	}
}
