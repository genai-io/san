package subagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

func (r *preparedRun) interactionSource(ctx context.Context) tool.AgentInteractionSource {
	var origin *tool.WorkflowOrigin
	if r.req.Workflow != nil {
		copy := *r.req.Workflow
		origin = &copy
	}
	return tool.AgentInteractionSource{
		Workflow: origin,
		AgentID:  r.hookID, TaskID: r.req.TaskID, AgentName: r.cfg.displayName,
		ParentToolCallID: r.req.ParentToolCallID,
		ToolCallID:       core.ToolCallIDFromContext(ctx), WorkingDirectory: r.currentCwd(),
		PermissionMode: string(r.cfg.permMode),
	}
}

func (e *Executor) interactionApproval(run *preparedRun) perm.PermissionFunc {
	return func(ctx context.Context, name string, input map[string]any) (bool, string) {
		if err := ctx.Err(); err != nil {
			return false, err.Error()
		}
		source := run.interactionSource(ctx)
		// Tool arguments are JSON values. Make an independent, deep snapshot
		// before handing them to a UI that may retain them asynchronously.
		raw, err := json.Marshal(input)
		if err != nil {
			return false, fmt.Sprintf("cannot prepare %s approval: %v", name, err)
		}
		var snapshot map[string]any
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return false, fmt.Sprintf("cannot prepare %s approval: %v", name, err)
		}
		request := &perm.PermissionRequest{ToolName: name, Description: "Execute " + name}
		if resolved, ok := tool.Get(name); ok {
			if aware, ok := resolved.(tool.PermissionAwareTool); ok {
				rich, err := aware.PreparePermission(ctx, input, source.WorkingDirectory)
				if err != nil {
					return false, fmt.Sprintf("cannot prepare %s approval: %v", name, err)
				}
				if rich != nil {
					request = rich
				}
			}
		}
		request.ID = tool.GenerateRequestID()
		request.ToolName = name
		request.CallerAgent = source.Label()
		request.WorkingDirectory = source.WorkingDirectory
		request.PermissionMode = displayPermissionMode(run.cfg.permMode)
		request.AllowRules = nil
		request.OnceOnly = true
		approved, reason := e.interactions.Approve(ctx, source, request, snapshot)
		if err := ctx.Err(); err != nil {
			return false, err.Error()
		}
		return approved, reason
	}
}
