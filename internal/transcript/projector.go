package transcript

import (
	"fmt"
	"iter"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func Project(records []Record) (*Transcript, error) {
	return project(records, true)
}

// project folds the records into a transcript. A resume reads the
// conversation as it stands: a ConversationReplaced (a reset in the log, which
// is how compaction is written) starts it over. History keeps every message the
// session ever held, each once, in the order it first appeared.
func project(records []Record, resume bool) (*Transcript, error) {
	return projectRecords(func(yield func(Record, error) bool) {
		for _, r := range records {
			if !yield(r, nil) {
				return
			}
		}
	}, resume)
}

func projectRecords(records iter.Seq2[Record, error], resume bool) (*Transcript, error) {
	t := &Transcript{}
	seen := make(map[string]bool)
	for r, err := range records {
		if err != nil {
			return nil, err
		}
		switch r.Type {
		case SessionStarted:
			applyStarted(t, r)
		case SessionForked:
			applyForked(t, r)
		case ConversationReplaced:
			if err := validateMessages(r.Messages); err != nil {
				return nil, err
			}
			if resume {
				t.Messages = nil
				clear(seen)
			}
			for _, m := range r.Messages {
				t.Messages, seen[m.ID] = appendNew(t.Messages, seen, m), true
			}
			if t.UpdatedAt.Before(r.Time) {
				t.UpdatedAt = r.Time
			}
		case MessageAppended:
			if r.Message == nil {
				return nil, fmt.Errorf("message.appended missing payload")
			}
			t.Messages, seen[r.Message.ID] = appendNew(t.Messages, seen, *r.Message), true
			if t.CreatedAt.IsZero() {
				t.CreatedAt = r.Time
			}
			if t.UpdatedAt.Before(r.Time) {
				t.UpdatedAt = r.Time
			}
		case InferenceRequested, InferenceResponded:
			if r.Inference != nil {
				if r.Inference.Model != "" {
					t.Model = r.Inference.Model
				}
				if r.Inference.Provider != "" {
					t.Provider = r.Inference.Provider
				}
			}
		case SessionStatePatched:
			if r.State == nil {
				return nil, fmt.Errorf("state payload is required")
			}
			r.State.applyTo(&t.State)
			if t.ID == "" {
				t.ID = r.SessionID
			}
			if t.UpdatedAt.Before(r.Time) {
				t.UpdatedAt = r.Time
			}
		case ToolCompleted:
			if r.ToolDetails != nil {
				if t.ToolDetails == nil {
					t.ToolDetails = make(map[string]ToolDetailsRecord)
				}
				t.ToolDetails[r.ToolDetails.ToolCallID] = *r.ToolDetails
			}
		case NoticeAdded:
			if r.Notice != nil {
				t.Notices = append(t.Notices, *r.Notice)
			}
		case SessionCompacted:
			if t.ID == "" {
				t.ID = r.SessionID
			}
			if t.UpdatedAt.Before(r.Time) {
				t.UpdatedAt = r.Time
			}
		}
	}
	return t, nil
}

// appendNew appends m unless a message with its ID is already there.
func appendNew(msgs []ai.Message, seen map[string]bool, m ai.Message) []ai.Message {
	if m.ID != "" && seen[m.ID] {
		return msgs
	}
	return append(msgs, m)
}

func applyStarted(t *Transcript, r Record) {
	if t.ID == "" {
		t.ID = r.SessionID
	}
	if t.Cwd == "" {
		t.Cwd = r.Cwd
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = r.Time
	}
	if t.UpdatedAt.Before(r.Time) {
		t.UpdatedAt = r.Time
	}
	if r.Start != nil {
		t.Provider = r.Start.Provider
		t.Model = r.Start.Model
		t.ParentSessionID = r.Start.ParentSessionID
	}
}
func applyForked(t *Transcript, r Record) {
	if t.ID == "" {
		t.ID = r.SessionID
	}
	if t.UpdatedAt.Before(r.Time) {
		t.UpdatedAt = r.Time
	}
	if r.Fork != nil {
		t.ParentSessionID = r.Fork.ParentSessionID
		t.CreatedAt = r.Time
	}
}
