// Permission approval flow + gate response. The approval modal lives in
// the input package; here we handle the user's decision (once / for-session
// / persist-as-rule) and forward it through the PermissionGate that gates
// the agent's tool calls.
package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/ask"
	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/infra/log"
)

type permissionDecision = ask.PermissionDecision

func (m *model) handlePermGateDecision(decision permissionDecision) tea.Cmd {
	pending := m.userPrompts.active
	if !m.validUserPrompt(pending) || pending.Permission == nil || !m.userPrompts.shown || decision.Request != m.userPrompts.permission {
		return nil
	}
	var noticeCmd tea.Cmd
	if !m.userPrompts.approval.Resolve(m.conv.AgentToUI, decision, func(name string) {
		if m.env.SessionPermissions != nil {
			m.env.SessionPermissions.AllowTool(name)
		}
	}, func(rules []string) {
		noticeCmd = m.saveAllowRules(rules)
	}) {
		return nil
	}
	if decision.Approved && pending.Source.AgentID != "" {
		m.conv.Tool.MarkStarted(pending.Source.ParentToolCallID)
	}
	m.finishUserPrompt()
	return tea.Batch(noticeCmd, m.advanceUserPrompts())
}

// saveAllowRules attempts persistence and reports the outcome without revoking approval.
func (m *model) saveAllowRules(rules []string) tea.Cmd {
	notice := "Always allowed " + strings.Join(rules, ", ")
	if path, err := m.services.Setting.AddLocalAllowRules(m.env.CWD, rules); err != nil {
		log.Logger().Warn("save always-allow rule failed", zap.Error(err))
		notice = "Approved once; could not save the allow rule: " + err.Error()
	} else {
		notice += " in " + widgets.ShortenPathForProject(path, m.env.CWD)
	}
	return m.deliverNotice(mainNotice{Display: notice})
}
