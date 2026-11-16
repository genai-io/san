package subagent

import (
	"context"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
)

func (r *preparedRun) interactionSource(ctx context.Context) tool.AgentInteractionSource {
	var origin *tool.FlowOrigin
	if r.req.Flow != nil {
		copy := *r.req.Flow
		origin = &copy
	}
	// Interaction names are UI identity, not the normalized runtime name.
	name := r.cfg.displayName
	if r.cfg.config != nil && r.cfg.config.Name != "" {
		name = r.cfg.config.Name
	}
	return tool.AgentInteractionSource{
		Flow:    origin,
		AgentID: r.hookID, TaskID: r.req.TaskID, AgentName: name,
		ParentToolCallID: r.req.ParentToolCallID,
		ToolCallID:       core.ToolCallIDFromContext(ctx), WorkingDirectory: r.currentCwd(),
		PermissionMode: string(r.cfg.permMode),
	}
}
