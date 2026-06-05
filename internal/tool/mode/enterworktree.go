package mode

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/san/internal/worktree"
)

// EnterWorktreeTool switches the main conversation into a git worktree
// for safe experimentation. The user must approve.
type EnterWorktreeTool struct{}

func (t *EnterWorktreeTool) Name() string { return "EnterWorktree" }
func (t *EnterWorktreeTool) Description() string {
	return "Switch into a git worktree for safe experimentation"
}
func (t *EnterWorktreeTool) Icon() string { return "T" }

// RequiresInteraction returns true — needs user confirmation.
func (t *EnterWorktreeTool) RequiresInteraction() bool { return true }

// PrepareInteraction returns the request for the TUI.
func (t *EnterWorktreeTool) PrepareInteraction(_ context.Context, params map[string]any, cwd string) (any, error) {
	return &tool.EnterWorktreeRequest{Slug: tool.GetString(params, "name")}, nil
}

// ExecuteWithResponse handles the TUI's approval.
func (t *EnterWorktreeTool) ExecuteWithResponse(_ context.Context, params map[string]any, response any, cwd string) toolresult.ToolResult {
	resp, ok := response.(*tool.EnterWorktreeResponse)
	if !ok || !resp.Approved {
		return toolresult.ToolResult{
			Success:  true,
			Output:   "User declined entering worktree. Continue working in the current directory.",
			Metadata: toolresult.ResultMetadata{Title: t.Name(), Icon: t.Icon()},
		}
	}

	return toolresult.ToolResult{
		Success: true,
		Output: fmt.Sprintf("Switched to worktree at %s.\n"+
			"All file operations now target this isolated copy.\n"+
			"Use ExitWorktree when done to return to the original directory.",
			resp.WorktreePath),
		HookResponse: map[string]any{
			"worktreePath": resp.WorktreePath,
			"action":       "enter",
		},
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: resp.WorktreePath,
		},
	}
}

// Execute is the non-interactive fallback (auto-approve for subagents).
func (t *EnterWorktreeTool) Execute(_ context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	result, cleanup, err := worktree.Create(cwd, tool.GetString(params, "name"))
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}
	_ = cleanup // worktree lifecycle managed by agent executor

	return toolresult.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Created worktree at %s", result.Path),
		HookResponse: map[string]any{
			"worktreePath": result.Path,
			"action":       "enter",
		},
		Metadata: toolresult.ResultMetadata{Title: t.Name(), Icon: t.Icon(), Subtitle: result.Path},
	}
}

func init() {
	tool.Register(&EnterWorktreeTool{})
}
