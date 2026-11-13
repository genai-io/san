package session

import (
	"errors"
	"reflect"
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestAcceptedInputOrderReplayAndFork(t *testing.T) {
	store := cleanStore(t)
	owner := createTyped(t, store, CreateOptions{})
	first := eventText("first", ai.RoleUser, "first request")
	if err := owner.Append(t.Context(), Event{Payload: MessageAppendPayload{Messages: []ai.Message{first}}}); err != nil {
		t.Fatal(err)
	}
	before := owner.Snapshot()
	turn, err := owner.StartInference(t.Context(), InferenceOptions{Purpose: PurposeTurn, TurnID: "turn", Provider: "test", Model: ai.Model{ID: "model"}, Request: conversationRequest(&before, nil)})
	if err != nil {
		t.Fatal(err)
	}
	queued := eventText("queued", ai.RoleUser, "exact prepared input")
	if err := owner.AcceptInput(t.Context(), queued, nil); err != nil {
		t.Fatal(err)
	}
	accepted := owner.Snapshot()
	acceptedSeq := uint64(len(accepted.Events()))
	answer := eventText("answer", ai.RoleAssistant, "first answer")
	if err := turn.Finish(t.Context(), &ai.Response{Content: answer.Content}, nil, Event{Payload: MessageAppendPayload{Messages: []ai.Message{answer}}}); err != nil {
		t.Fatal(err)
	}
	p := owner.Snapshot()
	want := []ai.Message{first, answer, queued}
	if !reflect.DeepEqual(p.ActiveMessages(), want) || !reflect.DeepEqual(p.Messages(), want) {
		t.Fatalf("queued input overtook output: %+v", p.ActiveMessages())
	}
	// The next native boundary consumes the already-owned message without
	// publishing duplicate history or losing the original acceptance evidence.
	recorder := NewRecorder(owner, "test")
	recorder.RecordEvent(sdkagent.MessageAdded{Message: queued})
	if err := recorder.Err(); err != nil {
		t.Fatal(err)
	}
	p = owner.Snapshot()
	events := p.Events()
	last := events[len(events)-1]
	if last.Source == nil || last.Source.Session != "" || last.Source.Seq != acceptedSeq || last.Payload.(*MessageAppendPayload).Accepted {
		t.Fatalf("lost consumption source: %+v", last)
	}
	if len(p.Messages()) != 3 || len(p.data.Accepted) != 0 {
		t.Fatal("consumption duplicated history or stayed pending")
	}
	next, err := owner.StartInference(t.Context(), InferenceOptions{Purpose: PurposeTurn, TurnID: "turn", Provider: "test", Model: ai.Model{ID: "model"}, Request: conversationRequest(&p, nil)})
	if err != nil {
		t.Fatal(err)
	}
	p = owner.Snapshot()
	start := p.Events()[next.Ref().Seq-1].Payload.(*InferenceStartPayload)
	if start.Input.Conversation == nil || start.Input.Standalone != "" {
		t.Fatal("ordering mismatch was hidden by standalone fallback")
	}
	if err := next.Finish(t.Context(), nil, errors.New("cancelled")); err != nil {
		t.Fatal(err)
	}
	p = owner.Snapshot()
	// A fork before consumption recovers the accepted tail; a complete fork and
	// a reopened log recover exactly the same native order and pending state.
	for _, seq := range []uint64{acceptedSeq, 0} {
		fork, err := store.Fork(t.Context(), owner.ID(), ForkOptions{Seq: seq})
		if err != nil {
			t.Fatal(err)
		}
		fp := fork.Snapshot()
		if seq == 0 {
			if !reflect.DeepEqual(fp.ActiveMessages(), want) || len(fp.data.Accepted) != 0 {
				t.Fatal("full fork lost consumed order")
			}
		} else if !reflect.DeepEqual(fp.ActiveMessages(), accepted.ActiveMessages()) || len(fp.data.Accepted) != 1 {
			t.Fatal("prefix fork lost queued input")
		}
		if err := fork.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), owner.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed := reopened.Snapshot()
	if !reflect.DeepEqual(replayed.ActiveMessages(), p.ActiveMessages()) || !reflect.DeepEqual(replayed.Events(), p.Events()) {
		t.Fatal("replay changed accepted/consumed history")
	}
}

func TestAcceptedInputResumeConsumesRequestAtomically(t *testing.T) {
	store := cleanStore(t)
	owner := createTyped(t, store, CreateOptions{})
	queued := eventText("queued", ai.RoleUser, "accepted before shutdown")
	if err := owner.AcceptInput(t.Context(), queued, nil); err != nil {
		t.Fatal(err)
	}
	request := &ai.Request{Messages: []ai.Message{queued}}
	turn, err := owner.StartInference(t.Context(), InferenceOptions{Purpose: PurposeTurn, TurnID: "turn", Provider: "test", Model: ai.Model{ID: "model"}, Request: request})
	if err != nil {
		t.Fatal(err)
	}
	p := owner.Snapshot()
	if len(p.data.Accepted) != 0 || p.Events()[turn.Ref().Seq-1].Payload.(*InferenceStartPayload).Input.Conversation == nil {
		t.Fatal("resumed request did not consume accepted input before reconstruction")
	}
	batches := recorderBatches(t, store, owner)
	if last := batches[len(batches)-1]; !reflect.DeepEqual(last, []string{"message.append", "inference.start"}) {
		t.Fatalf("consume/start not atomic: %v", last)
	}
	answer := eventText("answer", ai.RoleAssistant, "after resumed input")
	if err := turn.Finish(t.Context(), &ai.Response{Content: answer.Content}, nil, Event{Payload: MessageAppendPayload{Messages: []ai.Message{answer}}}); err != nil {
		t.Fatal(err)
	}
	p = owner.Snapshot()
	if !reflect.DeepEqual(p.ActiveMessages(), []ai.Message{queued, answer}) {
		t.Fatal("resumed output overtook restored input")
	}
}

func TestAcceptedInputRejectsChangedDuplicateAndPreservesOnCompaction(t *testing.T) {
	store := cleanStore(t)
	owner := createTyped(t, store, CreateOptions{})
	queued := eventText("queued", ai.RoleUser, "original")
	if err := owner.AcceptInput(t.Context(), queued, nil); err != nil {
		t.Fatal(err)
	}
	p := owner.Snapshot()
	seq := uint64(len(p.Events()))
	changed := queued
	changed.Content = ai.TextContent("changed")
	for _, event := range []Event{
		{Payload: MessageAppendPayload{Messages: []ai.Message{queued}}},
		{Source: &EventRef{Seq: seq}, Payload: MessageAppendPayload{Messages: []ai.Message{changed}}},
		{Source: &EventRef{Seq: seq}, Payload: MessageAppendPayload{Messages: []ai.Message{queued}, Accepted: true}},
	} {
		if err := owner.Append(t.Context(), event); err == nil {
			t.Fatal("invalid consumption accepted")
		}
	}
	if err := owner.Append(t.Context(), Event{Payload: ContextCompactPayload{Summary: eventText("summary", ai.RoleUser, "summary"), Through: seq}}); err != nil {
		t.Fatal(err)
	}
	p = owner.Snapshot()
	if got := p.ActiveMessages(); len(got) != 2 || got[1].ID != queued.ID || len(p.data.Accepted) != 1 {
		t.Fatal("compaction dropped accepted pending input")
	}
}

func TestCompletionAcceptMessageFinishesAndOwnsInputAtomically(t *testing.T) {
	store := cleanStore(t)
	owner := createTyped(t, store, CreateOptions{})
	scope := owner.Completion(PurposeAutopilot)
	finish, err := scope.Admission()(t.Context(), "test", ai.Model{ID: "model"}, &ai.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(t.Context(), &ai.Response{Content: ai.TextContent("utility output")}, nil); err != nil {
		t.Fatal(err)
	}
	message := eventText("prepared", ai.RoleUser, "prepared utility input")
	if err := scope.AcceptMessage(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	p := owner.Snapshot()
	if len(p.Unfinished().Inferences) != 0 || len(p.data.Accepted) != 1 || !reflect.DeepEqual(p.ActiveMessages(), []ai.Message{message}) {
		t.Fatal("utility acceptance did not finish and own exact input")
	}
	batches := recorderBatches(t, store, owner)
	if last := batches[len(batches)-1]; !reflect.DeepEqual(last, []string{"message.append", "inference.finish"}) {
		t.Fatalf("accept/finish not atomic: %v", last)
	}
}
