package interaction

import (
	"encoding/json"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/tool/perm"
)

// PermissionDecision selects a native approval scope, not a transport option ID.
type PermissionDecision struct {
	Approved bool
	AllowAll bool
	Persist  bool
	Request  *perm.PermissionRequest
}

// Permission owns one displayed approval; its caller serializes presentation and replies.
// Adapters must map only offered options and pass their current session owner.
type Permission struct {
	asked    perm.Asked
	request  *Request
	view     *perm.PermissionRequest
	resolved bool
	onceOnly bool
	rules    []string
	toolName string
}

// NewPermission snapshots the displayed question before execution resumes.
// The options callback derives display labels after worker scope is restricted.
func NewPermission(req *Request, owner Owner, view *perm.PermissionRequest, options func(*perm.PermissionRequest) []string) *Permission {
	if !req.Valid(owner) || req.Permission == nil || view == nil {
		return nil
	}
	if req.Source.AgentID != "" {
		view.OnceOnly = true
		view.AllowRules = nil
	}
	p := &Permission{request: req, view: view,
		onceOnly: view.OnceOnly, rules: append([]string(nil), view.AllowRules...), toolName: view.ToolName}
	p.asked = perm.Asked{Detail: append(json.RawMessage(nil), PermissionDetail(req, view)...), At: time.Now().UnixMilli()}
	if options != nil {
		p.asked.Options = append([]string(nil), options(view)...)
	}
	req.Permission.MarkAsked(p.asked)
	return p
}

// Resolve validates identity and scope, applies grants, and replies to the caller.
// Persistence outcomes do not change approval; the adapter reports them without rollback.
func (p *Permission) Resolve(owner Owner, d PermissionDecision, allowTool func(string), persist func([]string)) bool {
	if p == nil || p.resolved || !p.request.Valid(owner) || d.Request != p.view {
		return false
	}
	if p.onceOnly {
		d.AllowAll, d.Persist = false, false
	}
	if d.Persist && (len(p.rules) == 0 || persist == nil) {
		return false
	}
	if d.AllowAll && !d.Persist && allowTool == nil {
		return false
	}
	// Claim before callbacks so a reentrant or duplicate reply cannot grant twice.
	p.resolved = true
	reason := "user denied"
	if d.Approved {
		reason = "user approved"
		if d.Persist {
			if persist != nil {
				persist(append([]string(nil), p.rules...))
			}
		} else if d.AllowAll && allowTool != nil {
			allowTool(p.toolName)
		}
	}
	scope := "once"
	if d.Persist {
		scope = "persistent"
	} else if d.AllowAll {
		scope = "session"
	}
	// Cancellation during a callback cannot authorize the retired caller.
	asked := p.asked
	asked.Options = append([]string(nil), asked.Options...)
	asked.Detail = append(json.RawMessage(nil), asked.Detail...)
	response := agent.PermGateResponse{Allow: d.Approved, Reason: reason, By: "user", Scope: scope, Asked: &asked}
	if !p.request.Valid(owner) {
		response = agent.PermGateResponse{Reason: "interaction cancelled", By: "user", Cancelled: true, Asked: &asked}
	}
	select {
	case p.request.Permission.Response <- response:
	default:
	}
	return true
}
