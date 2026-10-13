package transcript

import (
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/log"
)

type StartCommand struct {
	SessionID       string
	Cwd             string
	Provider        string
	Model           string
	MaxTokens       int
	AgentID         string
	ParentSessionID string
	Time            time.Time
}

type ForkCommand struct {
	SourceSessionID string
	NewSessionID    string
	Time            time.Time
}

type ListOptions struct {
	Limit            int
	IncludeSidechain bool
}

func PatchTitle(title string) PatchOp       { return mustPatch(PatchPathTitle, title) }
func PatchLastPrompt(prompt string) PatchOp { return mustPatch(PatchPathLastPrompt, prompt) }
func PatchTag(tag string) PatchOp           { return mustPatch(PatchPathTag, tag) }
func PatchMode(mode string) PatchOp         { return mustPatch(PatchPathMode, mode) }
func PatchAutoPilot(v string) PatchOp       { return mustPatch(PatchPathAutoPilot, v) }
func PatchGroup(v string) PatchOp           { return mustPatch(PatchPathGroup, v) }
func PatchTasks(tasks []TodoItemSnapshot) PatchOp {
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
	if prev.Group != next.Group {
		ops = append(ops, PatchGroup(next.Group))
	}
	if !tasksEqual(prev.Tasks, next.Tasks) {
		ops = append(ops, PatchTasks(next.Tasks))
	}
	if !worktreeEqual(prev.Worktree, next.Worktree) {
		ops = append(ops, PatchWorktree(next.Worktree))
	}
	return ops
}

func tasksEqual(a, b []TodoItemSnapshot) bool {
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
