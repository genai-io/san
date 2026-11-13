package subagent

import (
	"context"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

type childPermission struct {
	config     *AgentConfig
	mode       PermissionMode
	child      *session.Session
	parent     func(*session.Session, bool, string) tool.Resolver
	background bool
}

func (p childPermission) CanApproveByHook(string, map[string]any) bool { return false }
func (p childPermission) Check(ctx context.Context, name string, input map[string]any, forced bool, reason string) (bool, string) {
	result := p.Resolve(ctx, name, input, forced, reason)
	return result.Decision == "allow", result.Reason
}
func (p childPermission) Resolve(ctx context.Context, name string, input map[string]any, forced bool, reason string) perm.Resolution {
	// Local policy may narrow the owner, but never supplies an owner's authority.
	var prompt perm.PermissionFunc
	if p.parent != nil {
		prompt = func(context.Context, string, map[string]any) (bool, string) { return true, "owner review" }
	}
	allowed, why := subagentPermissionFuncWithPrompt(p.mode, p.config.AllowTools, p.config.DenyTools, prompt)(ctx, name, input)
	by := "mode"
	if p.config.DenyTools.Matches(name, input) || setting.CircuitBreakerReason(name, input) != "" || tool.IsParentOnlyTool(name) {
		by = "config"
	} else if p.mode != PermissionExplore && operationMode(p.mode) != setting.ModeBypassPermissions {
		if confirmation, _ := setting.ConfirmationTier(name, input); confirmation == "" && (p.config.AllowTools.Matches(name, input) || p.config.AllowTools.HasName(name)) {
			by = "config"
		}
	}
	if !allowed {
		return perm.Resolution{Decision: "deny", By: by, Reason: why}
	}
	if p.parent != nil {
		cwd, _ := ctx.Value(executionDirectoryKey{}).(string)
		owner := p.parent(p.child, p.background, cwd)
		if owner == nil {
			return perm.Resolution{Decision: "deny", By: "config", Reason: "owner permission resolver unavailable"}
		}
		return owner.Resolve(ctx, name, input, forced, reason)
	}
	if forced {
		return perm.Resolution{Decision: "deny", By: "hook", Reason: "worker hook requires explicit approval: " + reason}
	}
	return perm.Resolution{Decision: "allow", By: by, Reason: why}
}
