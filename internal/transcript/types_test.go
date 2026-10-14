package transcript

import (
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestTranscriptTypesCarryProjectedState(t *testing.T) {
	now := time.Date(2026, 4, 6, 12, 30, 0, 0, time.UTC)
	tr := Transcript{
		ID:              "tx-1",
		ParentSessionID: "tx-0",
		Cwd:             "/tmp/project",
		CreatedAt:       now,
		UpdatedAt:       now,
		Provider:        "openai",
		Model:           "gpt-test",
		Messages:        []ai.Message{{ID: "msg-1", Role: ai.RoleAssistant, Content: ai.TextContent("hello")}},
		State: State{
			Title:      "Fix persistence",
			LastPrompt: "continue",
			Tasks: []todo.Item{{
				ID:      "1",
				Subject: "Refactor store",
				Status:  "in_progress",
			}},
		},
	}

	if tr.Messages[0].Role != "assistant" {
		t.Fatalf("unexpected role: %q", tr.Messages[0].Role)
	}
	if tr.State.Tasks[0].Subject != "Refactor store" {
		t.Fatalf("unexpected task subject: %q", tr.State.Tasks[0].Subject)
	}
}

func TestProjectedTypesDoNotExposeJSONTags(t *testing.T) {
	fields := []reflect.StructField{
		reflect.TypeFor[Transcript]().Field(0),
		reflect.TypeFor[State]().Field(0),
		reflect.TypeFor[Summary]().Field(0),
	}

	for _, field := range fields {
		if tag := field.Tag.Get("json"); tag != "" {
			t.Fatalf("%s unexpectedly has json tag %q", field.Name, tag)
		}
	}
}

func TestMetadataHelpers(t *testing.T) {
	now := time.Date(2026, 4, 6, 12, 30, 0, 0, time.UTC)
	meta := MetadataFromTranscript(&Transcript{
		ID:              "tx-1",
		ParentSessionID: "parent-1",
		Cwd:             "/tmp/project",
		CreatedAt:       now,
		UpdatedAt:       now.Add(time.Minute),
		Provider:        "openai",
		Model:           "gpt-test",
		Messages:        []ai.Message{{ID: "m1"}},
		State: State{
			Title:      "Title",
			LastPrompt: "continue",
			Mode:       "plan",
		},
	})
	if meta.ID != "tx-1" || meta.ParentSessionID != "parent-1" || meta.MessageCount != 1 {
		t.Fatalf("unexpected metadata projection: %+v", meta)
	}

}
