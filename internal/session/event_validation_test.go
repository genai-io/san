package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestEventRequiredWireFieldsAndDuplicateKeys(t *testing.T) {
	f := newEventFixture(t)
	wire, _ := json.Marshal(f.p.Events()[0])
	for name, change := range map[string]func(string) string{
		"missing timestamp": func(s string) string { return strings.Replace(s, `"created_at":123,`, "", 1) },
		"missing sidechain": func(s string) string { return strings.Replace(s, `,"sidechain":false`, "", 1) },
		"null sidechain":    func(s string) string { return strings.Replace(s, `"sidechain":false`, `"sidechain":null`, 1) },
		"duplicate id":      func(s string) string { return strings.Replace(s, `"id":`, `"id":"ignored","id":`, 1) },
		"duplicate payload member": func(s string) string {
			return strings.Replace(s, `"sidechain":false`, `"sidechain":true,"sidechain":false`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var e Event
			if err := json.Unmarshal([]byte(change(string(wire))), &e); err == nil {
				t.Fatal("ambiguous or incomplete wire accepted")
			}
		})
	}
}

func TestEventNativeInvocationAmbiguityAndRoles(t *testing.T) {
	for name, m := range map[string]ai.Message{
		"ambiguous call":   {ID: "bad", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "same", Name: "Read", Input: "{}"}), ai.ToolCallBlock(ai.ToolCall{ID: "same", Name: "Write", Input: "{\"different\":true}"})}},
		"user call":        {ID: "bad", Role: ai.RoleUser, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "call", Name: "Read", Input: "{}"})}},
		"assistant result": {ID: "bad", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", ToolName: "Read"})}},
	} {
		t.Run(name, func(t *testing.T) {
			f, start := fixtureThrough(t, EventInferenceStart)
			e := f.next(MessageAppendPayload{Messages: []ai.Message{m}}, start.ID, "")
			if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
				t.Fatal("invalid native invocation accepted")
			}
		})
	}
	f, start := fixtureThrough(t, EventToolStart)
	result := ai.Message{ID: "mismatched-result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", ToolName: "Write"})}}
	f.add(t, MessageAppendPayload{Messages: []ai.Message{result}}, start.ID, "")
	compact := f.next(CompactionApplyPayload{SourceSeq: uint64(len(f.p.Events())), Summary: eventText("summary", ai.RoleAssistant, "summary"), RetainedMessageIDs: []string{"call-message", "mismatched-result"}}, "", "")
	if _, err := ApplyEventBatch(f.p, []Event{compact}, f.ctx); err == nil {
		t.Fatal("compaction paired different tool names")
	}
}

func fixtureThrough(t *testing.T, typ EventType) (*eventFixture, Event) {
	t.Helper()
	full := fullEventFixture(t)
	f := &eventFixture{ctx: full.ctx}
	for _, batch := range full.batches {
		for _, e := range batch {
			f.batch(t, e)
			if e.Type == typ {
				return f, e
			}
		}
	}
	t.Fatal("fixture type not found")
	return nil, Event{}
}

func TestEventTerminalReferencesAndOutcomes(t *testing.T) {
	for name, mutate := range map[string]func(*Event){
		"missing source":              func(e *Event) { e.SourceID = "" },
		"wrong family":                func(e *Event) { e.SourceID = "event-1" },
		"unknown outcome":             func(e *Event) { e.Outcome = "unknown" },
		"missing outcome":             func(e *Event) { e.Outcome = "" },
		"failure missing error":       func(e *Event) { e.Outcome = OutcomeFailure },
		"cancellation missing reason": func(e *Event) { e.Outcome = OutcomeCancelled },
		"wrong call":                  func(e *Event) { e.Payload = ToolFinishPayload{CallID: "wrong"} },
	} {
		t.Run(name, func(t *testing.T) {
			f, start := fixtureThrough(t, EventToolStart)
			e := f.next(ToolFinishPayload{CallID: "call"}, start.ID, OutcomeSuccess)
			mutate(&e)
			if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
				t.Fatal("invalid finish accepted")
			}
		})
	}
	for _, outcome := range []EventOutcome{OutcomeFailure, OutcomeCancelled} {
		t.Run(string(outcome), func(t *testing.T) {
			f, start := fixtureThrough(t, EventToolStart)
			payload := ToolFinishPayload{CallID: "call"}
			if outcome == OutcomeFailure {
				payload.Error = &EventError{Code: "failed", Message: "bounded diagnostic"}
			} else {
				payload.Reason = "user cancelled"
			}
			f.add(t, payload, start.ID, outcome)
		})
	}
}

func TestEventPermissionAndDurableInputReferences(t *testing.T) {
	f, request := fixtureThrough(t, EventPermissionRequest)
	e := f.next(PermissionDecidePayload{Decision: "allow", Scope: PermissionScope{Resources: []string{"another-file"}}}, request.ID, "")
	if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
		t.Fatal("scope expansion accepted")
	}
	f.add(t, PermissionDecidePayload{Decision: "deny", Scope: PermissionScope{Resources: []string{"file"}}}, request.ID, "")
	e = f.next(PermissionDecidePayload{Decision: "allow", Scope: PermissionScope{Resources: []string{"file"}}}, request.ID, "")
	if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
		t.Fatal("duplicate decision accepted")
	}
	for name, ref := range map[string]InputRef{
		"missing event": {EventID: "missing", MessageID: "user", CallID: "call"},
		"wrong owner":   {EventID: "event-1", MessageID: "call-message", CallID: "call"},
		"missing call":  {EventID: "event-9", MessageID: "call-message", CallID: "wrong"},
	} {
		t.Run(name, func(t *testing.T) {
			e := f.next(ToolStartPayload{CallID: ref.CallID, InputRef: ref}, "", "")
			if _, err := ApplyEventBatch(f.p, []Event{e}, f.ctx); err == nil {
				t.Fatal("invalid durable input accepted")
			}
		})
	}
}

func TestEventCreationAndForeignEvidence(t *testing.T) {
	f := newEventFixture(t)
	create := f.p.Events()[0]
	payload := *create.Payload.(*SessionCreatePayload)
	payload.ParentSessionID = "parent"
	payload.ForkSourceID = "source"
	payload.ForkSourceSeq = 2
	create.Payload = payload
	ctx := EventValidationContext{ParentAncestors: map[string][]string{"parent": {"parent"}}, ForkPrefixes: map[string]uint64{"source": 2}}
	if _, err := ApplyEventBatch(nil, []Event{create}, ctx); err != nil {
		t.Fatal(err)
	}
	ctx.ParentAncestors["parent"] = []string{"parent", "session"}
	if _, err := ApplyEventBatch(nil, []Event{create}, ctx); err == nil {
		t.Fatal("cyclic parent accepted")
	}
	if _, err := ApplyEventBatch(nil, []Event{create}, EventValidationContext{}); err == nil {
		t.Fatal("unverified ancestry accepted")
	}
	full := fullEventFixture(t)
	if _, err := ReplayEventBatches(full.batches, EventValidationContext{}); err == nil {
		t.Fatal("unresolved foreign provenance silently accepted")
	}
	// Replay uses captured evidence only, not a foreign stream or runtime callback.
	if _, err := ReplayEventBatches(full.batches, full.ctx); err != nil {
		t.Fatal(err)
	}
}
