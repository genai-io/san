package transcript

import (
	"fmt"
	"slices"

	"github.com/genai-io/sdk-go/pkg/ai"
)

type messageNode struct {
	ai.Message
	ParentMessageID string
}

func Project(records []Record) (*Transcript, error) {
	return project(records, true)
}

// project keeps the same parent chain for both resume and history; only resume
// stops at the latest compaction boundary.
func project(records []Record, stopAtCompaction bool) (*Transcript, error) {
	t := &Transcript{}
	messageMap := make(map[string]messageNode, len(records))
	order := make([]string, 0, len(records))
	var compactBoundary string

	for _, r := range records {
		switch r.Type {
		case SessionStarted:
			applyStarted(t, r)
		case SessionForked:
			applyForked(t, r)
		case ConversationReplaced:
			if err := validateMessages(r.Messages); err != nil {
				return nil, err
			}
			messageMap = make(map[string]messageNode, len(r.Messages))
			order = nil
			compactBoundary = ""
			parent := ""
			for _, m := range r.Messages {
				messageMap[m.ID] = messageNode{Message: m, ParentMessageID: parent}
				order = append(order, m.ID)
				parent = m.ID
			}
			if t.UpdatedAt.Before(r.Time) {
				t.UpdatedAt = r.Time
			}
		case MessageAppended:
			n, err := buildNode(r)
			if err != nil {
				return nil, err
			}
			messageMap[n.ID] = n
			order = append(order, n.ID)
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
			if stopAtCompaction && r.Compaction != nil {
				compactBoundary = r.Compaction.MessageID
			}
			if t.ID == "" {
				t.ID = r.SessionID
			}
			if t.UpdatedAt.Before(r.Time) {
				t.UpdatedAt = r.Time
			}
		}
	}

	t.Messages = materializeActiveChain(messageMap, order, compactBoundary)
	return t, nil
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

func buildNode(r Record) (messageNode, error) {
	if r.Message == nil {
		return messageNode{}, fmt.Errorf("message.appended missing payload")
	}
	return messageNode{Message: *r.Message, ParentMessageID: r.ParentMessageID}, nil
}

func materializeActiveChain(messageMap map[string]messageNode, order []string, boundary string) []ai.Message {
	if len(order) == 0 {
		return nil
	}

	hasChild := make(map[string]bool, len(order))
	for _, id := range order {
		n := messageMap[id]
		if n.ParentMessageID != "" {
			hasChild[n.ParentMessageID] = true
		}
	}

	var leafID string
	for _, id := range slices.Backward(order) {

		if !hasChild[id] {
			leafID = id
			break
		}
	}
	if leafID == "" {
		// All messages have children — likely a corrupted transcript with cycles.
		// Fall back to the last message in insertion order to avoid silent data loss.
		leafID = order[len(order)-1]
	}

	reversed := make([]ai.Message, 0, len(order))
	cur := leafID
	seen := make(map[string]bool, len(order))
	for cur != "" {
		if seen[cur] {
			break
		}
		seen[cur] = true

		n, ok := messageMap[cur]
		if !ok {
			break
		}
		reversed = append(reversed, n.Message)

		if boundary != "" && cur == boundary {
			break
		}
		cur = n.ParentMessageID
	}

	slices.Reverse(reversed)
	return reversed
}
