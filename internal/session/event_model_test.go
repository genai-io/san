package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

const testSession = "session"

type eventSpec struct {
	payload EventPayload
	source  *EventRef
}

func spec(p EventPayload) eventSpec { return eventSpec{payload: p} }
func from(seq uint64, p EventPayload) eventSpec {
	return eventSpec{payload: p, source: &EventRef{Seq: seq}}
}

type eventFixture struct {
	p       *Projection
	batches [][]Event
	ctx     EventValidationContext
}

func (f *eventFixture) tail() uint64 {
	if f.p == nil {
		return 0
	}
	return uint64(len(f.p.data.Events))
}

// events assigns contiguous sequence numbers after the current tail.
func (f *eventFixture) events(specs ...eventSpec) []Event {
	out := make([]Event, len(specs))
	for i, s := range specs {
		out[i] = Event{Seq: f.tail() + uint64(i) + 1, Type: s.payload.eventType(), SchemaVersion: 1, Time: 123, Source: s.source, Payload: s.payload}
	}
	return out
}
func (f *eventFixture) commit(t *testing.T, specs ...eventSpec) []Event {
	t.Helper()
	events := f.events(specs...)
	p, err := ApplyEventBatch(f.p, testSession, events, f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.p = p
	f.batches = append(f.batches, events)
	return events
}
func newEventFixture(t *testing.T) *eventFixture {
	f := &eventFixture{ctx: EventValidationContext{ForeignEvents: map[string]ForeignEventEvidence{
		foreignKey("child", 1): {Type: EventMessageAppend, MessageIDs: []string{"original"}},
	}}}
	f.commit(t, spec(SessionCreatePayload{ProjectID: "project", Workspace: "/work", InitialCWD: "/work/sub", AgentID: "agent"}))
	return f
}
func eventText(id string, role ai.Role, text string) ai.Message {
	return ai.Message{ID: id, Role: role, Content: ai.TextContent(text)}
}
func (f *eventFixture) turnStart(purpose string) InferenceStartPayload {
	ins, tools := f.p.CatalogueDigests()
	provider, model := "provider", "model"
	return InferenceStartPayload{Purpose: purpose, TurnID: "turn", Request: EventConfig{Provider: &provider, Model: &model},
		Input: InferenceInput{Conversation: &ConversationInput{}}, InstructionsDigest: ins, ToolsetDigest: tools}
}

const standaloneDigest = "0000000000000000000000000000000000000000000000000000000000000001"

// fullEventFixture uses all thirteen event types in one session.
func fullEventFixture(t *testing.T) *eventFixture {
	f := newEventFixture(t)
	f.commit(t, spec(InstructionsUpdatePayload{Set: map[string]InstructionSection{"base": {Content: "Be helpful"}}, Cause: "boot"}))
	f.commit(t, spec(ToolsetUpdatePayload{Set: map[string]ai.Schema{"Read": {Name: "Read", Description: "read"}}, Cause: "boot"}))
	f.commit(t, spec(SkillsUpdatePayload{Set: map[string]string{"review": SkillActive}, Cause: "user:/skills"}))
	f.commit(t, spec(SessionUpdatePayload{Config: &ConfigPatch{Model: SetField("model")}, State: &StatePatch{Title: SetField("title")}}))
	f.commit(t, spec(MessageAppendPayload{Messages: []ai.Message{eventText("user", ai.RoleUser, "question")}}))
	start := f.commit(t, spec(f.turnStart(PurposeTurn)))[0]
	call := ai.Message{ID: "call-message", Role: ai.RoleAssistant, Content: ai.Content{ai.ThinkingBlock("thought", "signature"), ai.ReasoningBlock(ai.ReasoningItem{ID: "reason", EncryptedContent: "opaque"}), ai.ToolCallBlock(ai.ToolCall{ID: "call", Name: "Read", Input: `{"path":"file"}`, Signature: []byte("signed")})}}
	f.commit(t,
		from(start.Seq, MessageAppendPayload{Messages: []ai.Message{call}}),
		from(start.Seq, InferenceFinishPayload{Outcome: OutcomeSuccess}),
		spec(PermissionDecidePayload{CallID: "call", Decision: "allow", By: ByConfig}),
	)
	result := ai.Message{ID: "tool-result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", ToolName: "Read", Content: ai.Content{ai.TextBlock("before"), ai.ImageBlock(ai.Image{MediaType: "image/png", Data: "aGVsbG8="}), ai.TextBlock("after")}})}}
	f.commit(t, spec(MessageAppendPayload{Messages: []ai.Message{result}}), spec(ToolFinishPayload{CallID: "call", Outcome: OutcomeSuccess, ChildSession: "child"}))
	f.commit(t, eventSpec{MessageAppendPayload{Messages: []ai.Message{eventText("received", ai.RoleAssistant, "child output")}}, &EventRef{Session: "child", Seq: 1}})
	f.commit(t, spec(HookFinishPayload{Hook: "audit", Trigger: "completion", Outcome: HookRan}))
	f.commit(t, spec(AnnotationAddPayload{Text: "note", AfterMessage: "user"}))
	provider, model := "provider", "model"
	cstart := f.commit(t, spec(InferenceStartPayload{Purpose: PurposeCompaction, Request: EventConfig{Provider: &provider, Model: &model}, Input: InferenceInput{Standalone: standaloneDigest}}))[0]
	through := f.tail()
	f.commit(t,
		from(cstart.Seq, ContextCompactPayload{Summary: eventText("summary", ai.RoleUser, "summary"), Through: through}),
		from(cstart.Seq, InferenceFinishPayload{Outcome: OutcomeSuccess}),
	)
	return f
}

func TestEventFullReplayAndNativeRoundTrip(t *testing.T) {
	f := fullEventFixture(t)
	types := map[EventType]bool{}
	for _, e := range f.p.Events() {
		types[e.Type] = true
	}
	if len(types) != 13 {
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
		p, err := ReplayEventBatches(testSession, batches, f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p, f.p) {
			t.Fatal("replay changed projection")
		}
	}
	messages := f.p.Messages()
	active := f.p.ActiveMessages()
	if len(messages) != 5 || len(active) != 1 || active[0].ID != "summary" || messages[0].ID != "user" {
		t.Fatalf("compaction must keep history and shrink context: %d/%d", len(messages), len(active))
	}
	call := messages[1]
	if call.Content[0].Signature != "signature" || call.Content[1].Reasoning.EncryptedContent != "opaque" || string(call.Content[2].ToolCall.Signature) != "signed" {
		t.Fatal("native signatures lost")
	}
	blocks := messages[2].Content[0].ToolResult.Content
	if len(blocks) != 3 || blocks[1].Image.Data != "aGVsbG8=" {
		t.Fatal("native images/order lost")
	}
	if f.p.State().Title == nil || *f.p.State().Title != "title" || *f.p.Config().Model != "model" || f.p.Skills()["review"] != SkillActive {
		t.Fatal("compaction reset state/config")
	}
	if u := f.p.Unfinished(); len(u.Inferences)+len(u.NeverRan)+len(u.Unknown) != 0 {
		t.Fatalf("nothing is unfinished: %+v", u)
	}
}

func TestEventBatchValidationIsAllOrNone(t *testing.T) {
	f := newEventFixture(t)
	before := f.p.Events()
	events := f.events(spec(SessionUpdatePayload{State: &StatePatch{Title: SetField("new")}}), spec(AnnotationAddPayload{}))
	if got, err := ApplyEventBatch(f.p, testSession, events, f.ctx); err == nil || got != nil {
		t.Fatal("published invalid batch")
	}
	if !reflect.DeepEqual(before, f.p.Events()) || f.p.State().Title != nil {
		t.Fatal("mutated base")
	}
}

func TestEventMalformedValidation(t *testing.T) {
	envelope := map[string]func(*Event){
		"sequence": func(e *Event) { e.Seq++ }, "schema": func(e *Event) { e.SchemaVersion = 2 },
		"missing source": func(e *Event) { e.Source = &EventRef{Seq: 99} }, "type": func(e *Event) { e.Type = EventToolFinish },
		"foreign unresolved": func(e *Event) { e.Source = &EventRef{Session: "child", Seq: 9} },
		"own session":        func(e *Event) { e.Source = &EventRef{Session: testSession, Seq: 1} },
	}
	for name, change := range envelope {
		t.Run(name, func(t *testing.T) {
			f := newEventFixture(t)
			e := f.events(spec(AnnotationAddPayload{Text: "hello"}))[0]
			change(&e)
			if _, err := ApplyEventBatch(f.p, testSession, []Event{e}, f.ctx); err == nil {
				t.Fatal("accepted malformed event")
			}
		})
	}
	f := fullEventFixture(t)
	badStart := func(change func(*InferenceStartPayload)) EventPayload {
		s := f.turnStart(PurposeTurn)
		change(&s)
		return s
	}
	for name, s := range map[string]eventSpec{
		"duplicate message":  spec(MessageAppendPayload{Messages: []ai.Message{eventText("user", ai.RoleUser, "duplicate")}}),
		"assistant no src":   spec(MessageAppendPayload{Messages: []ai.Message{eventText("a", ai.RoleAssistant, "x")}}),
		"bad anchor":         spec(AnnotationAddPayload{Text: "note", AfterMessage: "nope"}),
		"empty update":       spec(SessionUpdatePayload{}),
		"unchanged update":   spec(SessionUpdatePayload{Config: &ConfigPatch{Model: SetField("model")}}),
		"catalogue overlap":  spec(InstructionsUpdatePayload{Set: map[string]InstructionSection{"x": {Content: "c"}}, Remove: []string{"x"}, Cause: "boot"}),
		"catalogue no cause": spec(InstructionsUpdatePayload{Set: map[string]InstructionSection{"x": {Content: "c"}}}),
		"unknown removal":    spec(ToolsetUpdatePayload{Remove: []string{"Nope"}, Cause: "boot"}),
		"tool key mismatch":  spec(ToolsetUpdatePayload{Set: map[string]ai.Schema{"A": {Name: "B"}}, Cause: "boot"}),
		"bad skill state":    spec(SkillsUpdatePayload{Set: map[string]string{"s": "maybe"}, Cause: "boot"}),
		"compact through":    spec(ContextCompactPayload{Summary: eventText("s2", ai.RoleUser, "s"), Through: f.tail() + 1}),
		"unknown purpose":    spec(badStart(func(s *InferenceStartPayload) { s.Purpose = "chat" })),
		"turn without id":    spec(badStart(func(s *InferenceStartPayload) { s.TurnID = "" })),
		"both inputs":        spec(badStart(func(s *InferenceStartPayload) { s.Input.Standalone = standaloneDigest })),
		"no input":           spec(badStart(func(s *InferenceStartPayload) { s.Input = InferenceInput{} })),
		"stale digest":       spec(badStart(func(s *InferenceStartPayload) { s.ToolsetDigest = "stale" })),
		"bad standalone":     spec(badStart(func(s *InferenceStartPayload) { s.Input = InferenceInput{Standalone: "short"} })),
		"unknown call":       spec(PermissionDecidePayload{CallID: "missing", Decision: "allow", By: ByUser}),
		"decided twice":      spec(PermissionDecidePayload{CallID: "call", Decision: "deny", By: ByUser}),
		"finish no gate":     spec(ToolFinishPayload{CallID: "missing", Outcome: OutcomeSuccess}),
		"finish twice":       spec(ToolFinishPayload{CallID: "call", Outcome: OutcomeSuccess}),
		"hook blocked":       spec(HookFinishPayload{Hook: "h", Trigger: "t", Outcome: HookBlocked}),
		"finish no source":   spec(InferenceFinishPayload{Outcome: OutcomeSuccess}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ApplyEventBatch(f.p, testSession, f.events(s), f.ctx); err == nil {
				t.Fatal("accepted malformed payload")
			}
		})
	}
	var start uint64
	for _, e := range f.p.Events() {
		if e.Type == EventInferenceStart {
			start = e.Seq
			break
		}
	}
	if _, err := ApplyEventBatch(f.p, testSession, f.events(from(start, InferenceFinishPayload{Outcome: OutcomeSuccess})), f.ctx); err == nil {
		t.Fatal("duplicate finish accepted")
	}
	if _, err := ApplyEventBatch(f.p, testSession, f.events(eventSpec{InferenceFinishPayload{Outcome: OutcomeSuccess}, &EventRef{Session: "child", Seq: 1}}), f.ctx); err == nil {
		t.Fatal("foreign fake start accepted")
	}
}

func TestEventGateAndUnfinished(t *testing.T) {
	f := newEventFixture(t)
	f.commit(t, spec(MessageAppendPayload{Messages: []ai.Message{eventText("user", ai.RoleUser, "go")}}))
	start := f.commit(t, spec(f.turnStart(PurposeTurn)))[0]
	two := ai.Message{ID: "calls", Role: ai.RoleAssistant, Content: ai.Content{
		ai.ToolCallBlock(ai.ToolCall{ID: "ran", Name: "Read", Input: `{}`}), ai.ToolCallBlock(ai.ToolCall{ID: "never", Name: "Read", Input: `{}`}), ai.ToolCallBlock(ai.ToolCall{ID: "denied", Name: "Read", Input: `{}`})}}
	f.commit(t, from(start.Seq, MessageAppendPayload{Messages: []ai.Message{two}}))
	f.commit(t, spec(PermissionDecidePayload{CallID: "ran", Decision: "allow", By: ByGrant}), spec(PermissionDecidePayload{CallID: "denied", Decision: "deny", By: ByUser, Asked: &Asked{Options: []string{"allow", "deny"}}}))
	u := f.p.Unfinished()
	if !reflect.DeepEqual(u, Unfinished{Inferences: []uint64{start.Seq}, NeverRan: []string{"never"}, Unknown: []string{"ran"}}) {
		t.Fatalf("unfinished: %+v", u)
	}
	if _, err := ApplyEventBatch(f.p, testSession, f.events(spec(ToolFinishPayload{CallID: "denied", Outcome: OutcomeSuccess})), f.ctx); err == nil {
		t.Fatal("tool.finish for a denied call accepted")
	}
}

func TestEventCompactKeepsLaterMessagesAndPairs(t *testing.T) {
	f := newEventFixture(t)
	f.commit(t, spec(MessageAppendPayload{Messages: []ai.Message{eventText("u1", ai.RoleUser, "one")}}))
	through := f.tail()
	f.commit(t, spec(MessageAppendPayload{Messages: []ai.Message{eventText("u2", ai.RoleUser, "two")}}))
	f.commit(t, spec(ContextCompactPayload{Summary: eventText("s1", ai.RoleUser, "summary"), Through: through}))
	var ids []string
	for _, m := range f.p.ActiveMessages() {
		ids = append(ids, m.ID)
	}
	if !reflect.DeepEqual(ids, []string{"s1", "u2"}) || len(f.p.Messages()) != 3 {
		t.Fatalf("active %v: messages appended after through must stay", ids)
	}
	f.commit(t, spec(ContextCompactPayload{Summary: eventText("s2", ai.RoleUser, "again"), Through: f.tail()}))
	if len(f.p.ActiveMessages()) != 1 || len(f.p.Messages()) != 4 {
		t.Fatal("consecutive compaction lost history or kept context")
	}

	g := newEventFixture(t)
	start := g.commit(t, spec(g.turnStart(PurposeTurn)))[0]
	call := ai.Message{ID: "c", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "call", Name: "Read", Input: `{}`})}}
	g.commit(t, from(start.Seq, MessageAppendPayload{Messages: []ai.Message{call}}))
	through = g.tail()
	result := ai.Message{ID: "r", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", ToolName: "Read", Content: ai.TextContent("ok")})}}
	g.commit(t, spec(MessageAppendPayload{Messages: []ai.Message{result}}))
	if _, err := ApplyEventBatch(g.p, testSession, g.events(spec(ContextCompactPayload{Summary: eventText("s", ai.RoleUser, "s"), Through: through})), g.ctx); err == nil {
		t.Fatal("compaction that orphans a tool result accepted")
	}
}

func TestEventPatchTriStateAndStrictCodec(t *testing.T) {
	f := newEventFixture(t)
	f.commit(t, spec(SessionUpdatePayload{State: &StatePatch{Title: SetField("")}}))
	if s := f.p.State(); s.Title == nil || *s.Title != "" {
		t.Fatal("zero set lost")
	}
	f.commit(t, spec(SessionUpdatePayload{State: &StatePatch{Title: ClearField[string]()}}))
	if f.p.State().Title != nil {
		t.Fatal("clear lost")
	}
	e := f.events(spec(SessionUpdatePayload{Config: &ConfigPatch{Model: SetField("")}}))[0]
	b, _ := json.Marshal(e)
	for _, bad := range []string{strings.Replace(string(b), `"model":""`, `"secret":"password"`, 1), strings.Replace(string(b), `"config":{`, `"config":{"title":"bad",`, 1), strings.Replace(string(b), `"source"`, `"source_event"`, 1) + ` {}`} {
		var got Event
		if err := json.Unmarshal([]byte(bad), &got); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	f.commit(t, spec(e.Payload))
	if f.p.Config().Model == nil || *f.p.Config().Model != "" {
		t.Fatal("empty model set lost")
	}
}

func TestEventRequiredWireFieldsAndDuplicateKeys(t *testing.T) {
	f := newEventFixture(t)
	wire, _ := json.Marshal(f.p.Events()[0])
	for name, change := range map[string]func(string) string{
		"missing time":   func(s string) string { return strings.Replace(s, `"time":123,`, "", 1) },
		"missing seq":    func(s string) string { return strings.Replace(s, `"seq":1,`, "", 1) },
		"null workspace": func(s string) string { return strings.Replace(s, `"workspace":"/work"`, `"workspace":null`, 1) },
		"duplicate seq":  func(s string) string { return strings.Replace(s, `"seq":`, `"seq":9,"seq":`, 1) },
		"unknown type":   func(s string) string { return strings.Replace(s, `"session.create"`, `"session.start"`, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			var e Event
			if err := json.Unmarshal([]byte(change(string(wire))), &e); err == nil {
				t.Fatal("ambiguous or incomplete wire accepted")
			}
		})
	}
}

func TestEventDeepCopyIsolation(t *testing.T) {
	f := fullEventFixture(t)
	original := f.p.Events()
	events := f.p.Events()
	events[0].Payload.(*SessionCreatePayload).ProjectID = "mutated"
	messages := f.p.Messages()
	messages[1].Content[2].ToolCall.Signature[0] = 'X'
	messages[2].Content[0].ToolResult.Content[1].Image.Data = "changed"
	*f.p.State().Title = "changed"
	if !reflect.DeepEqual(original, f.p.Events()) || f.p.Messages()[1].Content[2].ToolCall.Signature[0] != 's' {
		t.Fatal("accessor mutation escaped")
	}
	input := f.events(spec(MessageAppendPayload{Messages: []ai.Message{eventText("fresh", ai.RoleUser, "text")}}))
	p, err := ApplyEventBatch(f.p, testSession, input, f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Payload.(MessageAppendPayload).Messages[0].Content[0].Text = "mutated"
	if p.Messages()[len(p.Messages())-1].Content[0].Text != "text" {
		t.Fatal("input mutation escaped")
	}
}

func TestEventNativeSchemaNumberFidelity(t *testing.T) {
	f := newEventFixture(t)
	const exact int64 = 9007199254740993
	schema := ai.Schema{Name: "Exact", Definition: map[string]any{"const": exact, "enum": []any{exact}}}
	e := f.events(spec(ToolsetUpdatePayload{Set: map[string]ai.Schema{"Exact": schema}, Cause: "boot"}))[0]
	wire, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err = json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if roundtrip, _ := json.Marshal(decoded); string(wire) != string(roundtrip) {
		t.Fatalf("native numeric wire changed: %s -> %s", wire, roundtrip)
	}
	f.commit(t, spec(e.Payload))
	_, digest := f.p.CatalogueDigests()
	copy := f.p.Tools()
	if number := copy[0].Definition.(map[string]any)["const"].(json.Number); number.String() != "9007199254740993" {
		t.Fatal("schema number rounded")
	}
	f.commit(t, spec(AnnotationAddPayload{Text: "clone state"}))
	if _, after := f.p.CatalogueDigests(); after != digest {
		t.Fatal("batch clone changed numeric digest")
	}
}

func TestEventProjectionBranchesOwnContainers(t *testing.T) {
	f := fullEventFixture(t)
	before := eventCopy(f.p.data)
	branch := func(text string) []Event {
		return f.events(
			spec(MessageAppendPayload{Messages: []ai.Message{eventText("branch-message", ai.RoleUser, text)}}),
			spec(InstructionsUpdatePayload{Set: map[string]InstructionSection{"base": {Content: text}}, Cause: text}),
			spec(ToolsetUpdatePayload{Set: map[string]ai.Schema{"Read": {Name: "Read", Description: text}}, Cause: text}),
			spec(SkillsUpdatePayload{Set: map[string]string{"review": SkillDisabled}, Cause: text}),
			spec(SessionUpdatePayload{State: &StatePatch{Title: SetField(text)}, Config: &ConfigPatch{Model: SetField(text)}}),
		)
	}
	var wg sync.WaitGroup
	got := map[string]*Projection{}
	var mu sync.Mutex
	for _, text := range []string{"left", "right"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := ApplyEventBatch(f.p, testSession, branch(text), f.ctx)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			got[text] = p
			mu.Unlock()
		}()
	}
	wg.Wait()
	for text, p := range got {
		if *p.State().Title != text || *p.Config().Model != text || p.Instructions()[0].Content != text || p.Skills()["review"] != SkillDisabled {
			t.Fatalf("branch %s lost its changes", text)
		}
	}
	if !reflect.DeepEqual(before, eventCopy(f.p.data)) {
		t.Fatal("applying a batch changed its base")
	}
}

func TestEventReplayDiscardsFailedBuilder(t *testing.T) {
	batches := projectionBenchmarkBatches(100, 64)
	batches[99][0].Payload = MessageAppendPayload{Messages: []ai.Message{eventText("message-1", ai.RoleUser, "duplicate")}}
	if p, err := ReplayEventBatches(testSession, batches, EventValidationContext{}); p != nil || err == nil {
		t.Fatal("published partial replay")
	}
}
