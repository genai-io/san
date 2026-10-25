package app

import (
	"fmt"

	"github.com/genai-io/san/internal/app/kit"
)

// interactionQueueContext counts only valid permissions. Questions and secrets
// retain their FIFO positions without being misreported as worker approvals.
func (m *model) interactionQueueContext() string {
	request := m.interactions.active
	if !m.validInteraction(request) || m.env.Height < 10 {
		return ""
	}
	waiting := m.pendingApprovalCount()
	if request.Permission != nil {
		waiting--
	}
	if waiting == 0 {
		if m.tasks.review != nil {
			return kit.HintLine("Current request · FIFO order")
		}
		return ""
	}
	label := "approvals"
	if waiting == 1 {
		label = "approval"
	}
	context := fmt.Sprintf("%d %s waiting · FIFO", waiting, label)
	if request.Permission != nil {
		context = fmt.Sprintf("%d other %s waiting", waiting, label)
	}
	return kit.HintLine(kit.TruncateText(context, max(1, m.env.Width-1)))
}
