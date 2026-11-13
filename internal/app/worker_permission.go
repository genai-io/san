package app

import (
	"context"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/interaction"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

type workerResolver struct {
	settings *setting.Settings
	grants   *setting.SessionPermissions
	memory   system.MemoryStores
	bridge   *conv.AgentToUI
	child    *session.Session
	cwd      string
	mode     setting.OperationMode
	source   tool.AgentInteractionSource
}

func newWorkerResolver(settings *setting.Settings, grants *setting.SessionPermissions, memory system.MemoryStores, bridge *conv.AgentToUI, child *session.Session, source tool.AgentInteractionSource) tool.Resolver {
	if source.Workflow != nil {
		workflow := *source.Workflow
		source.Workflow = &workflow
	}
	p := &workerResolver{settings: settings, grants: grants, memory: memory, bridge: bridge, child: child, cwd: source.WorkingDirectory, mode: setting.ModeNormal, source: source}
	if child != nil {
		snapshot := child.Snapshot()
		config := snapshot.Config()
		if config.Mode != nil {
			p.mode = setting.OperationModeFromString(*config.Mode)
		}
	}
	return p
}

func (p *workerResolver) Resolve(ctx context.Context, name string, input map[string]any, forced bool, reason string) perm.Resolution {
	if p.child == nil || p.settings == nil || p.grants == nil {
		return perm.Resolution{Decision: "deny", By: "config", Reason: "worker permission owner unavailable"}
	}
	if err := p.child.Err(); err != nil {
		return perm.Resolution{Err: err}
	}
	if err := ctx.Err(); err != nil {
		return perm.Resolution{Decision: "cancelled", By: "user", Reason: err.Error()}
	}
	grants := p.grants.Snapshot()
	// Grants remain live, but a later parent mode change cannot widen this child.
	grants.Mode = p.mode
	policy := agent.PermissionPolicy{Settings: p.settings, Session: grants, AutoMemory: p.memory}
	d := policy.Decide(name, input)
	if d.Decision == perm.Reject || d.Decision == perm.Permit && !forced {
		decision := "deny"
		if d.Decision == perm.Permit {
			decision = "allow"
		}
		return perm.Resolution{Decision: decision, By: d.By, Scope: d.Scope, Reason: d.Reason}
	}
	if p.bridge == nil {
		return perm.Resolution{Decision: "deny", By: "config", Reason: "worker approval UI unavailable"}
	}
	if forced {
		d.Description = reason
	}
	req := &agent.PermGateRequest{Context: ctx, RequestID: core.NewMessageID(), ToolCallID: core.ToolCallIDFromContext(ctx), ToolName: name, Description: d.Description, Input: input}
	view, err := interaction.PreparePermission(req, p.cwd, p.mode.PersistenceName(), p.settings, grants)
	if err != nil {
		return perm.Resolution{Decision: "deny", By: "config", Reason: err.Error()}
	}
	view.OnceOnly, view.AllowRules = true, nil
	source := p.source
	source.ToolCallID = req.ToolCallID
	return p.bridge.ResolveApproval(ctx, source, view, input)
}
