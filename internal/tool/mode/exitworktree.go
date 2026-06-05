package mode

import (
	"context"
	"fmt"

	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/toolresult"
	"github.com/genai-io/san/v2/internal/worktree"
)

// ExitWorktreeTool exits the current worktree session, optionally removing it.
type ExitWorktreeTool struct{}

func (t *ExitWorktreeTool) Name() string { return "ExitWorktree" }
func (t *ExitWorktreeTool) Description() string {
	return "Exit the current worktree and return to the original directory"
}
func (t *ExitWorktreeTool) Icon() string { return "T" }

// RequiresInteraction returns true — needs user confirmation.
func (t *ExitWorktreeTool) RequiresInteraction() bool { return true }

// PrepareInteraction returns the request for the TUI.
func (t *ExitWorktreeTool) PrepareInteraction(_ context.Context, params map[string]any, _ string) (any, error) {
	action := tool.GetString(params, "action")
	if action == "" {
		action = "remove"
	}
	if action != "keep" && action != "remove" {
		return nil, fmt.Errorf("action must be 'keep' or 'remove', got %q", action)
	}

	discardChanges := tool.GetBool(params, "discard_changes")

	return &tool.ExitWorktreeRequest{
		Action:         action,
		DiscardChanges: discardChanges,
	}, nil
}

// ExecuteWithResponse handles the TUI's approval.
func (t *ExitWorktreeTool) ExecuteWithResponse(_ context.Context, _ map[string]any, response any, _ string) toolresult.ToolResult {
	resp, ok := response.(*tool.ExitWorktreeResponse)
	if !ok || !resp.Approved {
		return toolresult.ToolResult{
			Success:  true,
			Output:   "User declined exiting worktree. Still in worktree session.",
			Metadata: toolresult.ResultMetadata{Title: t.Name(), Icon: t.Icon()},
		}
	}

	return toolresult.ToolResult{
		Success: true,
		Output: fmt.Sprintf("Exited worktree. Restored working directory to %s.",
			resp.RestoredPath),
		HookResponse: map[string]any{
			"restoredPath": resp.RestoredPath,
			"action":       "exit",
		},
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: "restored: " + resp.RestoredPath,
		},
	}
}

// Execute is the non-interactive fallback.
func (t *ExitWorktreeTool) Execute(_ context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	baseCwd, ok := worktree.OriginalPath(cwd)
	if !ok {
		return toolresult.NewErrorResult(t.Name(), "ExitWorktree requires an active managed worktree session")
	}

	action := tool.GetString(params, "action")
	if action == "" {
		action = "remove"
	}
	if action != "keep" && action != "remove" {
		return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("action must be 'keep' or 'remove', got %q", action))
	}

	if action == "remove" {
		discardChanges := tool.GetBool(params, "discard_changes")
		if worktree.HasUncommittedChanges(cwd) && !discardChanges {
			return toolresult.NewErrorResult(t.Name(), "worktree has uncommitted changes; retry with discard_changes=true or use action=keep")
		}
		if err := worktree.Remove(baseCwd, cwd); err != nil {
			return toolresult.NewErrorResult(t.Name(), err.Error())
		}
	}

	return toolresult.ToolResult{
		Success: true,
		Output: fmt.Sprintf("Exited worktree. Restored working directory to %s.",
			baseCwd),
		HookResponse: map[string]any{
			"restoredPath": baseCwd,
			"action":       "exit",
			"mode":         action,
		},
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: "restored: " + baseCwd,
		},
	}
}

func init() {
	tool.Register(&ExitWorktreeTool{})
}
