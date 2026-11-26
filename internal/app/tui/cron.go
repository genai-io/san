package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const maxCronQueueSize = 100

type cronTickMsg struct{}

func startCronTicker() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return cronTickMsg{} })
}

func (m *model) handleCronTick() tea.Cmd {
	next := startCronTicker()
	scheduler := m.services.Cron
	if scheduler == nil || (scheduler.Empty() && len(m.cronQueue) == 0) {
		return next
	}
	idle := !m.conv.Stream.Active
	fired := scheduler.Tick()
	var prompt string
	for i, job := range fired {
		if idle && i == 0 {
			prompt = job.Prompt
		} else if len(m.cronQueue) < maxCronQueueSize {
			m.cronQueue = append(m.cronQueue, job.Prompt)
		}
	}
	if idle && len(fired) == 0 && len(m.cronQueue) > 0 {
		prompt, m.cronQueue = m.cronQueue[0], m.cronQueue[1:]
	}
	if prompt == "" {
		return next
	}
	return tea.Batch(next, m.injectCronPrompt(prompt))
}
