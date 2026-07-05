package transcript

import (
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/todo"
)

type StartCommand struct {
	SessionID string
	ProjectID string
	Cwd       string
	Provider  string
	Model     string
	MaxTokens int
	AgentID   string
	ParentID  string
	Time      time.Time
}

type AppendMessageCommand struct {
	SessionID   string
	MessageID   string
	ParentID    string
	Time        time.Time
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
	SessionID        string
	Time             time.Time
	SummaryMessageID string
}

type ForkCommand struct {
	SourceSessionID string
	NewSessionID    string
	Time            time.Time
}

// AppendInferenceCommand writes either an inference.requested or
// inference.responded record. Type selects which; Record carries the payload.
// AgentID is not on the command: the recorder is single-agent and the agent
// ID lives on session.started.
type AppendInferenceCommand struct {
	SessionID string
	Time      time.Time
	Type      string // InferenceRequested or InferenceResponded
	Record    InferenceRecord
}

// AppendSystemSectionCommand writes system.section.added or
// system.section.removed. Removed sections drop Content from Record.
type AppendSystemSectionCommand struct {
	SessionID string
	Time      time.Time
	Type      string // SystemSectionAdded or SystemSectionRemoved
	Record    SystemSectionRecord
}

// AppendToolCommand writes tool.added or tool.removed.
type AppendToolCommand struct {
	SessionID string
	Time      time.Time
	Type      string // ToolAdded or ToolRemoved
	Record    ToolRecord
}

// AppendHookCommand writes one hook.fired record. One record per completed
// hook invocation; async hooks emit when they finish, not when they kick off.
type AppendHookCommand struct {
	SessionID string
	Time      time.Time
	Record    HookRecord
}

// AppendSkillStateCommand writes one skill.state.changed record.
type AppendSkillStateCommand struct {
	SessionID string
	Time      time.Time
	Record    SkillRecord
}

// AppendPermissionCommand writes permission.required or permission.decided.
// Type selects which; Record carries the (shared) payload. Required is
// emitted once when an external resolver must adjudicate; decided is emitted
// once when the final allow/reject lands.
type AppendPermissionCommand struct {
	SessionID string
	Time      time.Time
	Type      string // PermissionRequired or PermissionDecided
	Record    PermissionRecord
}

type ListOptions struct {
	Limit            int
	IncludeSidechain bool
}

func PatchTitle(title string) PatchOp       { return mustPatch(PatchPathTitle, title) }
func PatchLastPrompt(prompt string) PatchOp { return mustPatch(PatchPathLastPrompt, prompt) }
func PatchTag(tag string) PatchOp           { return mustPatch(PatchPathTag, tag) }
func PatchMode(mode string) PatchOp         { return mustPatch(PatchPathMode, mode) }
func PatchAutoPilot(v string) PatchOp      { return mustPatch(PatchPathAutoPilot, v) }
func PatchTasks(tasks []todo.Task) PatchOp {
	return mustPatch(PatchPathTasks, tasks)
}
func PatchWorktree(worktree *WorktreeState) PatchOp { return mustPatch(PatchPathWorktree, worktree) }

// StateOpsDiff returns the patch ops for fields that changed between prev
// and next. Omitted ops keep the prior value (append-only last-wins);
// empty/nil values are valid and explicitly clear the field. First emit
// (prev == zero State) writes every non-default field.
func StateOpsDiff(prev, next State) []PatchOp {
	ops := make([]PatchOp, 0, 6)
	if prev.Title != next.Title {
		ops = append(ops, PatchTitle(next.Title))
	}
	if prev.LastPrompt != next.LastPrompt {
		ops = append(ops, PatchLastPrompt(next.LastPrompt))
	}
	if prev.Tag != next.Tag {
		ops = append(ops, PatchTag(next.Tag))
	}
	if prev.Mode != next.Mode {
		ops = append(ops, PatchMode(next.Mode))
	}
	if prev.AutoPilot != next.AutoPilot {
		ops = append(ops, PatchAutoPilot(next.AutoPilot))
	}
	if !tasksEqual(prev.Tasks, next.Tasks) {
		ops = append(ops, PatchTasks(TrackerTasksFromView(next.Tasks)))
	}
	if !worktreeEqual(prev.Worktree, next.Worktree) {
		ops = append(ops, PatchWorktree(next.Worktree))
	}
	return ops
}

func tasksEqual(a, b []TrackerTaskView) bool {
	if len(a) != len(b) {
		return false
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func worktreeEqual(a, b *WorktreeState) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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
