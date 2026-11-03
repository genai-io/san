// Permission approval flow + gate response. The approval modal lives in
// the input package; here we handle the user's decision (once / for-session
// / persist-as-rule) and forward it through the PermissionGate that gates
// the agent's tool calls.
package app

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/interaction"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/transcript"
)

type permissionDecision = interaction.PermissionDecision

func (m *model) handlePermGateDecision(decision permissionDecision) tea.Cmd {
	pending := m.interactions.active
	if !m.validInteraction(pending) || pending.Permission == nil || !m.interactions.shown || decision.Request != m.interactions.permission {
		return nil
	}
	var noticeCmd tea.Cmd
	if !m.interactions.approval.Resolve(m.conv.AgentToUI, decision, func(name string) {
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
	m.finishInteraction()
	return tea.Batch(noticeCmd, m.advanceInteractions())
}

func (m *model) recordPermission(record transcript.Record) {
	if rec := m.services.Session; rec != nil {
		_ = rec.Append(context.Background(), record)
	}
}

// saveAllowRules attempts persistence and reports the outcome without revoking approval.
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
