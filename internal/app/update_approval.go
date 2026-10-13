// Permission approval flow + gate response. The approval modal lives in
// the input package; here we handle the user's decision (once / for-session
// / persist-as-rule) and forward it through the PermissionGate that gates
// the agent's tool calls.
package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
)

type permissionDecision struct {
	Approved bool
	AllowAll bool // option 2: allow for the rest of the session
	Persist  bool // "Always allow": save Request.AllowRules
	Request  *perm.PermissionRequest
}

// Scope labels recorded for user-driven permission decisions. These names
// belong to the approval modal — the transcript schema treats them as opaque
// strings, so adding a new modal option (e.g. "this directory only") only
// requires a new label here, not a schema bump.
const (
	permScopeOnce       = "once"
	permScopeSession    = "session"
	permScopePersistent = "persistent"
)

// permDecisionFor maps the user's approve/reject bool to the transcript
// decision enum. Shared by the config-decided fast path (agent.go) and the
// user-decided ask path (this file).
func permDecisionFor(approved bool) string {
	if approved {
		return transcript.PermissionPermit
	}
	return transcript.PermissionReject
}

// permScope encodes which approval-modal option the user picked. Persist
// takes priority over AllowAll because the modal exposes them as
// mutually-exclusive radio-style choices.
func permScope(d permissionDecision) string {
	switch {
	case d.Persist:
		return permScopePersistent
	case d.AllowAll:
		return permScopeSession
	default:
		return permScopeOnce
	}
}

func (m *model) handlePermGateDecision(decision permissionDecision) tea.Cmd {
	pending := m.interactions.active
	if !m.validInteraction(pending) || pending.Permission == nil || !m.interactions.shown || decision.Request != m.interactions.permission {
		return nil
	}
	req := pending.Permission
	if m.interactions.permission.OnceOnly {
		decision.AllowAll, decision.Persist = false, false
	}
	var noticeCmd tea.Cmd
	reason := "user denied"
	if decision.Approved {
		reason = "user approved"
		callID := req.ToolCallID
		if pending.Source.AgentID != "" {
			callID = pending.Source.ParentToolCallID
		}
		m.conv.Tool.MarkStarted(callID)
		if decision.AllowAll && m.env.SessionPermissions != nil {
			m.env.SessionPermissions.AllowTool(decision.Request.ToolName)
		}
		if decision.Persist {
			noticeCmd = m.saveAllowRules(decision.Request.AllowRules)
		}
	}
	// Snapshot and record before waking the worker: tool input can acquire
	// runtime callbacks as soon as its gate resumes.
	permRecord := permDecisionRecord(req, decision, reason, pending.Source.PermissionMode)
	permRecord.Detail = interactionPermissionDetail(pending, decision.Request)
	if rec := m.services.Session.Writer(); rec != nil {
		rec.RecordPermissionDecided(permRecord)
	}
	select {
	case req.Response <- agent.PermGateResponse{Allow: decision.Approved, Reason: reason}:
	default:
	}
	m.finishInteraction()
	return tea.Batch(noticeCmd, m.advanceInteractions())
}

func permDecisionRecord(req *agent.PermGateRequest, decision permissionDecision, reason, mode string) transcript.PermissionRecord {
	return transcript.PermissionRecord{
		RequestID: req.RequestID,
		Tool:      req.ToolName,
		Input:     marshalPermInput(req.Input),
		Detail:    permDetail(decision.Request),
		Decision:  permDecisionFor(decision.Approved),
		Source:    transcript.PermissionSourceUser,
		Scope:     permScope(decision),
		Reason:    reason,
		Mode:      mode,
	}
}

// saveAllowRules persists an "Always allow"; a failed save leaves this one
// approval. Returns the command that shows the outcome.
func (m *model) saveAllowRules(rules []string) tea.Cmd {
	notice := "Always allowed " + strings.Join(rules, ", ")
	if path, err := m.services.Setting.AddLocalAllowRules(m.env.CWD, rules); err != nil {
		log.Logger().Warn("save always-allow rule failed", zap.Error(err))
		notice = "Approved once; could not save the allow rule: " + err.Error()
	} else {
		notice += " in " + kit.ShortenPathForProject(path, m.env.CWD)
	}
	return m.deliverNotice(mainNotice{Display: notice})
}
