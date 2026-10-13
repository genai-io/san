package transcript

import (
	"encoding/json"
	"fmt"
	"slices"
)

func Project(records []Record) (*Transcript, error) {
	return project(records, true)
}

// project keeps the same parent chain for both resume and history; only resume
// stops at the latest compaction boundary.
func project(records []Record, stopAtCompaction bool) (*Transcript, error) {
	t := &Transcript{}
	messageMap := make(map[string]MessageNode, len(records))
	order := make([]string, 0, len(records))
	var compactBoundary string

	for _, r := range records {
		switch r.Type {
		case SessionStarted:
			applyStarted(t, r)
		case SessionForked:
			applyForked(t, r)
		case ConversationReplaced:
			messageMap = make(map[string]MessageNode, len(r.Messages))
			order = nil
			compactBoundary = ""
			parent := ""
			for _, m := range r.Messages {
				messageMap[m.ID] = MessageNode{Message: m, ParentMessageID: parent, Time: r.Time}
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
			if err := applyStatePatch(&t.State, r.State); err != nil {
				return nil, err
			}
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

func buildNode(r Record) (MessageNode, error) {
	if r.Message == nil {
		return MessageNode{}, fmt.Errorf("message.appended missing payload")
	}
	return MessageNode{Message: *r.Message, ParentMessageID: r.ParentMessageID, Time: r.Time, Cwd: r.Cwd, GitBranch: r.GitBranch, AgentID: r.AgentID, IsSidechain: r.IsSidechain}, nil
}

func applyStatePatch(state *State, patch *StateRecord) error {
	if patch == nil {
		return nil
	}
	for _, op := range patch.Ops {
		switch op.Path {
		case PatchPathTitle:
			var v string
			if err := json.Unmarshal(op.Value, &v); err != nil {
				return fmt.Errorf("patch %s: %w", op.Path, err)
			}
			state.Title = v
		case PatchPathLastPrompt:
			var v string
			if err := json.Unmarshal(op.Value, &v); err != nil {
				return fmt.Errorf("patch %s: %w", op.Path, err)
			}
			state.LastPrompt = v
		case PatchPathTag:
			var v string
			if err := json.Unmarshal(op.Value, &v); err != nil {
				return fmt.Errorf("patch %s: %w", op.Path, err)
			}
			state.Tag = v
		case PatchPathMode:
			var v string
			if err := json.Unmarshal(op.Value, &v); err != nil {
				return fmt.Errorf("patch %s: %w", op.Path, err)
			}
			state.Mode = v
		case PatchPathAutoPilot:
			var v string
			if err := json.Unmarshal(op.Value, &v); err != nil {
				return fmt.Errorf("patch %s: %w", op.Path, err)
			}
			state.AutoPilot = v
		case PatchPathGroup:
			var v string
			if err := json.Unmarshal(op.Value, &v); err != nil {
				return fmt.Errorf("patch %s: %w", op.Path, err)
			}
			state.Group = v
		case PatchPathTasks:
			var tasks []TodoItemSnapshot
			if err := json.Unmarshal(op.Value, &tasks); err != nil {
				return fmt.Errorf("patch %s: %w", op.Path, err)
			}
			state.Tasks = tasks
		case PatchPathWorktree:
			if string(op.Value) == "null" {
				state.Worktree = nil
				continue
			}
			var wt WorktreeState
			if err := json.Unmarshal(op.Value, &wt); err != nil {
				return fmt.Errorf("patch %s: %w", op.Path, err)
			}
			state.Worktree = &wt
		default:
			return fmt.Errorf("unknown state path %q", op.Path)
		}
	}
	return nil
}

func materializeActiveChain(messageMap map[string]MessageNode, order []string, boundary string) []MessageNode {
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

	reversed := make([]MessageNode, 0, len(order))
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
		reversed = append(reversed, n)

		if boundary != "" && cur == boundary {
			break
		}
		cur = n.ParentMessageID
	}

	out := make([]MessageNode, 0, len(reversed))
	for _, r := range slices.Backward(reversed) {
		out = append(out, r)
	}
	return out
}
