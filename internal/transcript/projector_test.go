package transcript

import (
	"slices"
	"testing"
	"time"

	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestProjectStartAndAppendMessages(t *testing.T) {
	now := time.Date(2026, 4, 6, 14, 0, 0, 0, time.UTC)
	transcript, err := Project([]Record{
		{
			ID:        "tx-1:start",
			SessionID: "tx-1",
			Time:      now,
			Type:      SessionStarted,
			Cwd:       "/tmp/project",
			Start:     &SessionStart{Provider: "openai", Model: "gpt-test"},
		},
		{
			ID:        "rec-1",
			SessionID: "tx-1",
			Time:      now.Add(time.Second),
			Type:      MessageAppended,
			Message:   &ai.Message{ID: "msg-1", Role: "user", Content: ai.Content{ai.TextBlock("hello")}},
		},
		{
			ID:        "rec-2",
			SessionID: "tx-1",
			Time:      now.Add(2 * time.Second),
			Type:      MessageAppended,
			Message:   &ai.Message{ID: "msg-2", Role: "assistant", Content: ai.Content{ai.TextBlock("world")}},
		},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if transcript.ID != "tx-1" || transcript.Provider != "openai" || transcript.Model != "gpt-test" {
		t.Fatalf("unexpected transcript metadata: %+v", transcript)
	}
	if len(transcript.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(transcript.Messages))
	}
	if transcript.Messages[0].ID != "msg-1" || transcript.Messages[1].ID != "msg-2" {
		t.Fatalf("unexpected parent chain: %+v", transcript.Messages)
	}
}

func TestProjectStatePatchLastWins(t *testing.T) {
	transcript, err := Project([]Record{
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStarted},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StatePatch{Title: new("A"), Mode: new("normal")}},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StatePatch{Title: new("B"), Mode: new("plan")}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if transcript.State.Title != "B" || transcript.State.Mode != "plan" {
		t.Fatalf("unexpected state: %+v", transcript.State)
	}
}

func TestProjectAutoPilotRoundTrip(t *testing.T) {
	blob := `{"mission":"ship it","steers":{"turnEnd":true}}`
	tr, err := Project([]Record{
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStarted},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StatePatch{AutoPilot: new(blob)}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if tr.State.AutoPilot != blob {
		t.Fatalf("autoReview not restored: %q", tr.State.AutoPilot)
	}
	if MetadataFromTranscript(tr).AutoPilot != blob {
		t.Fatalf("autoReview missing from metadata view")
	}
}

func TestProjectTasksPatch(t *testing.T) {
	taskTime := time.Date(2026, 4, 6, 14, 10, 0, 0, time.UTC)
	task := todo.Item{
		ID:              "1",
		Subject:         "Refactor",
		Status:          "in_progress",
		Metadata:        map[string]any{"task_id": "bg-1"},
		CreatedAt:       taskTime,
		UpdatedAt:       taskTime,
		StatusChangedAt: taskTime,
	}
	transcript, err := Project([]Record{
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStarted},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StatePatch{Tasks: new([]todo.Item{task})}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if len(transcript.State.Tasks) != 1 || transcript.State.Tasks[0].Subject != "Refactor" {
		t.Fatalf("unexpected tasks: %+v", transcript.State.Tasks)
	}
	if transcript.State.Tasks[0].Metadata["task_id"] != "bg-1" {
		t.Fatalf("task metadata was not projected: %+v", transcript.State.Tasks[0].Metadata)
	}
}

func TestResumeStartsOverAtAResetAndHistoryKeepsEveryMessageOnce(t *testing.T) {
	msg := func(id string) Record {
		return Record{Type: MessageAppended, Message: &ai.Message{ID: id, Role: "user"}}
	}
	records := []Record{
		msg("start"), msg("reply"), msg("summary"),
		{Type: SessionCompacted, Compaction: &Compaction{MessageID: "summary"}},
		{Type: ConversationReplaced}, msg("summary"), msg("last"),
	}
	ids := func(tx *Transcript) []string {
		var out []string
		for _, m := range tx.Messages {
			out = append(out, m.ID)
		}
		return out
	}
	history, err := project(records, false)
	if err != nil || !slices.Equal(ids(history), []string{"start", "reply", "summary", "last"}) {
		t.Fatalf("history = %v, %v", ids(history), err)
	}
	resumed, err := Project(records)
	if err != nil || !slices.Equal(ids(resumed), []string{"summary", "last"}) {
		t.Fatalf("resume = %v, %v", ids(resumed), err)
	}
}
