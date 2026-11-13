package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func catalogueEvents(s *Session) []Event {
	p := s.Snapshot()
	return p.Events()
}

func TestRecorderCataloguesSnapshots(t *testing.T) {
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{})
	r := NewRecorder(s, "test")
	ctx := context.Background()
	ins := []NamedInstruction{{Name: "z", InstructionSection: InstructionSection{Content: "old"}}, {Name: "a", InstructionSection: InstructionSection{Content: "remove"}}}
	tools := []ai.Schema{{Name: "z"}, {Name: "a"}}
	skills := map[string]string{"z": SkillActive, "a": SkillEnabled}
	if err := r.RecordCatalogues(ctx, ins, tools, skills, "boot"); err != nil {
		t.Fatal(err)
	}
	ins = []NamedInstruction{{Name: "new", InstructionSection: InstructionSection{Content: "current"}}}
	tools = []ai.Schema{{Name: "new"}}
	skills = map[string]string{"new": SkillDisabled}
	if err := r.RecordCatalogues(ctx, ins, tools, skills, "resume"); err != nil {
		t.Fatal(err)
	}
	p := s.Snapshot()
	events := p.Events()
	if len(events) != 7 {
		t.Fatalf("events: %d", len(events))
	}
	for _, event := range events[4:] {
		var removed []string
		var cause string
		switch v := event.Payload.(type) {
		case *InstructionsUpdatePayload:
			removed, cause = v.Remove, v.Cause
		case *ToolsetUpdatePayload:
			removed, cause = v.Remove, v.Cause
		case *SkillsUpdatePayload:
			removed, cause = v.Remove, v.Cause
		default:
			t.Fatalf("unexpected %T", v)
		}
		if !reflect.DeepEqual(removed, []string{"a", "z"}) || cause != "resume" {
			t.Fatalf("removals/cause: %v %q", removed, cause)
		}
	}
	if err := r.RecordCatalogues(ctx, ins, tools, skills, "resume"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, catalogueEvents(s)) {
		t.Fatal("repeated snapshot changed events")
	}
	ins[0].Content = "mutated"
	tools[0].Name = "mutated"
	skills["new"] = SkillActive
	if !reflect.DeepEqual(events, catalogueEvents(s)) {
		t.Fatal("caller mutation escaped")
	}
	if len(p.Messages()) != 0 || len(p.ActiveMessages()) != 0 {
		t.Fatal("catalogues mutated context")
	}
	batches := 0
	for batch, err := range readEventBatches(ctx, store.sessionDir(s.ID())) {
		if err != nil {
			t.Fatal(err)
		}
		batches++
		if batches > 1 {
			decoded, err := decodeEventBatchPayload(batch.Batch.Records[0].Data)
			if err != nil || len(decoded.Events) != 3 {
				t.Fatalf("non-atomic catalogue batch: %+v %v", decoded, err)
			}
		}
	}
	if batches != 3 {
		t.Fatalf("batches: %d", batches)
	}
}

func TestRecorderCataloguesInvalid(t *testing.T) {
	valid := NamedInstruction{Name: "i", InstructionSection: InstructionSection{Content: "text"}}
	cases := []struct {
		name   string
		ins    []NamedInstruction
		tools  []ai.Schema
		skills map[string]string
		cause  string
	}{
		{name: "cause"},
		{name: "instruction name", ins: []NamedInstruction{{InstructionSection: InstructionSection{Content: "x"}}}, cause: "resume"},
		{name: "instruction duplicate", ins: []NamedInstruction{valid, valid}, cause: "resume"},
		{name: "instruction content", ins: []NamedInstruction{{Name: "i"}}, cause: "resume"},
		{name: "instruction slot", ins: []NamedInstruction{{Name: "i", InstructionSection: InstructionSection{Content: "x", Slot: -1}}}, cause: "resume"},
		{name: "tool name", tools: []ai.Schema{{}}, cause: "resume"},
		{name: "tool duplicate", tools: []ai.Schema{{Name: "t"}, {Name: "t"}}, cause: "resume"},
		{name: "skill name", skills: map[string]string{"": SkillActive}, cause: "resume"},
		{name: "skill state", skills: map[string]string{"s": "bad"}, cause: "resume"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := createTyped(t, cleanStore(t), CreateOptions{})
			r := NewRecorder(s, "test")
			before := catalogueEvents(s)
			if err := r.RecordCatalogues(context.Background(), tc.ins, tc.tools, tc.skills, tc.cause); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
			if r.Err() == nil {
				t.Fatal("error not latched")
			}
			if !reflect.DeepEqual(before, catalogueEvents(s)) {
				t.Fatal("partial write")
			}
		})
	}
}

func TestRecorderCataloguesFailure(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "storage"}[closed], func(t *testing.T) {
			s := createTyped(t, cleanStore(t), CreateOptions{})
			r := NewRecorder(s, "test")
			before := catalogueEvents(s)
			ctx := context.Background()
			want := ErrClosed
			if closed {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			if err := r.RecordCatalogues(ctx, nil, []ai.Schema{{Name: "t"}}, nil, "resume"); !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
			if !reflect.DeepEqual(before, catalogueEvents(s)) {
				t.Fatal("failure changed projection")
			}
			if !closed && r.Err() != nil {
				t.Fatal("context cancellation latched")
			}
		})
	}
}
