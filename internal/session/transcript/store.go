package transcript

import (
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/gen-code/internal/log"
	"github.com/genai-io/gen-code/internal/task/tracker"
)

type StartCommand struct {
	SessionID string
	ProjectID string
	Cwd       string
	Provider  string
	Model     string
	ParentID  string
	Time      time.Time
}

type AppendMessageCommand struct {
	SessionID   string
	MessageID   string
	ParentID    string
	Time        time.Time
	Cwd         string
	GitBranch   string
	AgentID     string
	IsSidechain bool
	Role        string
	Content     []ContentBlock
}

type PatchStateCommand struct {
	SessionID string
	Time      time.Time
	Ops       []PatchOp
}

type CompactCommand struct {
	SessionID  string
	Time       time.Time
	BoundaryID string
}

type ForkCommand struct {
	SourceSessionID string
	NewSessionID    string
	Time            time.Time
}

// AppendInferenceCommand writes either an inference.requested or
// inference.responded record. Type selects which; Record carries the payload.
type AppendInferenceCommand struct {
	SessionID string
	AgentID   string
	Time      time.Time
	Type      string // InferenceRequested or InferenceResponded
	Record    InferenceRecord
}

// AppendSystemSectionCommand writes system.section.added or
// system.section.removed. Removed sections drop Content from Record.
type AppendSystemSectionCommand struct {
	SessionID string
	AgentID   string
	Time      time.Time
	Type      string // SystemSectionAdded or SystemSectionRemoved
	Record    SystemSectionRecord
}

// AppendToolsCommand writes tools.added or tools.removed.
type AppendToolsCommand struct {
	SessionID string
	AgentID   string
	Time      time.Time
	Type      string // ToolsAdded or ToolsRemoved
	Record    ToolsRecord
}

type ListOptions struct {
	Limit            int
	IncludeSidechain bool
}

func PatchTitle(title string) PatchOp       { return mustPatch(PatchPathTitle, title) }
func PatchLastPrompt(prompt string) PatchOp { return mustPatch(PatchPathLastPrompt, prompt) }
func PatchTag(tag string) PatchOp           { return mustPatch(PatchPathTag, tag) }
func PatchMode(mode string) PatchOp         { return mustPatch(PatchPathMode, mode) }
func PatchTasks(tasks []tracker.Task) PatchOp {
	return mustPatch(PatchPathTasks, tasks)
}
func PatchWorktree(worktree *WorktreeState) PatchOp { return mustPatch(PatchPathWorktree, worktree) }

// StateOpsFor builds the full set of patch ops for a projected state.
// Used by the session save path to express the current snapshot as a single
// patch record. Zero-valued fields still produce ops (the projector applies
// last-wins, so re-patching empty values is safe).
func StateOpsFor(state State) []PatchOp {
	ops := []PatchOp{
		PatchTitle(state.Title),
		PatchLastPrompt(state.LastPrompt),
		PatchTag(state.Tag),
		PatchMode(state.Mode),
	}
	if len(state.Tasks) > 0 {
		ops = append(ops, PatchTasks(TrackerTasksFromView(state.Tasks)))
	}
	if state.Worktree != nil {
		ops = append(ops, PatchWorktree(state.Worktree))
	}
	return ops
}

func mustPatch(path string, v any) PatchOp {
	data, err := json.Marshal(v)
	if err != nil {
		// Log instead of panicking — the marshal input is always controlled
		// (strings, simple structs), so this should never happen in practice.
		log.Logger().Error("transcript: mustPatch marshal failed", zap.String("path", path), zap.Error(err))
		return PatchOp{Path: path, Value: []byte("null")}
	}
	return PatchOp{
		Path:  path,
		Value: data,
	}
}
