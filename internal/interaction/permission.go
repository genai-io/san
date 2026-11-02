package interaction

import (
	"encoding/json"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
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
	request  *Request
	view     *perm.PermissionRequest
	record   transcript.PermissionRecord
	resolved bool
	onRecord func(transcript.Record)
	onceOnly bool
	rules    []string
	toolName string
}

// NewPermission snapshots audit data before execution can mutate the tool input.
// The options callback derives display labels after worker scope is restricted.
func NewPermission(req *Request, owner Owner, view *perm.PermissionRequest, options func(*perm.PermissionRequest) []string, record func(transcript.Record)) *Permission {
	if !req.Valid(owner) || req.Permission == nil || view == nil {
		return nil
	}
	if req.Source.AgentID != "" {
		view.OnceOnly = true
		view.AllowRules = nil
	}
	p := &Permission{request: req, view: view, onRecord: record,
		onceOnly: view.OnceOnly, rules: append([]string(nil), view.AllowRules...), toolName: view.ToolName,
		record: transcript.PermissionRecord{
			RequestID: req.Permission.RequestID, Tool: req.Permission.ToolName,
			Input: PermissionInput(req.Permission.Input), Detail: PermissionDetail(req, view),
			Mode: req.Source.PermissionMode,
		}}
	required := p.record
	if options != nil {
		required.OptionsOffered = append([]string(nil), options(view)...)
	}
	required.Source = transcript.PermissionSourceAsk
	if record != nil {
		record(transcript.Record{Type: transcript.PermissionRequired, Permission: &required})
	}
	return p
}

// Resolve validates identity and scope, applies grants, and audits before waking the caller.
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
	decided := p.record
	decided.Decision = PermissionDecisionFor(d.Approved)
	decided.Source = transcript.PermissionSourceUser
	decided.Scope = "once"
	if d.Persist {
		decided.Scope = "persistent"
	} else if d.AllowAll {
		decided.Scope = "session"
	}
	decided.Reason = reason
	if p.onRecord != nil {
		p.onRecord(transcript.Record{Type: transcript.PermissionDecided, Permission: &decided})
	}
	// Cancellation during a callback cannot authorize the retired caller.
	response := agent.PermGateResponse{Allow: d.Approved, Reason: reason}
	if !p.request.Valid(owner) {
		response = agent.PermGateResponse{Reason: "interaction cancelled"}
	}
	select {
	case p.request.Permission.Response <- response:
	default:
	}
	return true
}

func PermissionDecisionFor(approved bool) string {
	if approved {
		return transcript.PermissionPermit
	}
	return transcript.PermissionReject
}

// PermissionInput owns serialized arguments before the gate resumes.
func PermissionInput(args map[string]any) json.RawMessage {
	if len(args) == 0 {
		return nil
	}
	data, err := json.Marshal(args)
	if err != nil {
		log.Logger().Warn("perm input marshal failed", zap.Error(err))
		return nil
	}
	return data
}
