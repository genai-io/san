package tool

import (
	"context"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// Tool represents a read-only tool that can be executed
type Tool interface {
	// Name returns the tool name
	Name() string

	// Description returns a brief description of the tool
	Description() string

	// Icon returns the tool icon emoji
	Icon() string

	// Schema returns the model-facing tool definition (name, description, and
	// JSON-schema parameters) sent to the LLM. Colocating it with the tool
	// keeps the wire contract and the implementation from drifting apart.
	Schema() core.ToolSchema

	// Execute runs the tool with the given parameters
	Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult
}

// PermissionAwareTool is a tool that requires user permission before execution
type PermissionAwareTool interface {
	Tool

	// RequiresPermission returns true if the tool needs user approval
	RequiresPermission() bool

	// PreparePermission prepares a permission request (e.g., computes diff)
	PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error)

	// ExecuteApproved executes the tool after user approval
	ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult
}

// InteractiveTool is a tool that requires user interaction (not just permission)
// Examples: AskUserQuestion for collecting user input
type InteractiveTool interface {
	Tool

	// RequiresInteraction returns true if the tool needs user interaction
	RequiresInteraction() bool

	// PrepareInteraction prepares an interaction request (e.g., question prompt)
	PrepareInteraction(ctx context.Context, params map[string]any, cwd string) (any, error)

	// ExecuteWithResponse executes the tool with the user's response
	ExecuteWithResponse(ctx context.Context, params map[string]any, response any, cwd string) toolresult.ToolResult
}
