package trigger

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/cron"
)

// Deps holds the app-level state and callbacks needed to process Source 3 input.
type Deps struct {
	StreamActive bool
	Cron         *cron.Scheduler
	InjectCron   func(string) tea.Cmd
	InjectHook   func(AsyncHookRewake) tea.Cmd
	AppendNotice func(string)
}

// Update routes Source 3 (system -> agent) messages.
func Update(deps Deps, state *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg.(type) {
	case CronTickMsg:
		return handleCronTick(deps, state), true
	case AsyncHookTickMsg:
		return handleAsyncHookTick(deps, state), true
	default:
		return nil, false
	}
}

func handleCronTick(deps Deps, state *Model) tea.Cmd {
	result := state.HandleCronTick(deps.Cron, !deps.StreamActive)

	cmds := []tea.Cmd{StartCronTicker()}
	if result.InjectPrompt != "" {
		cmds = append(cmds, deps.InjectCron(result.InjectPrompt))
	}
	for _, notice := range result.Notices {
		deps.AppendNotice(notice)
	}
	return tea.Batch(cmds...)
}

func handleAsyncHookTick(deps Deps, state *Model) tea.Cmd {
	cmds := []tea.Cmd{StartAsyncHookTicker()}

	item := state.HandleAsyncHookTick(!deps.StreamActive)
	if item == nil {
		return tea.Batch(cmds...)
	}

	cmds = append(cmds, deps.InjectHook(*item))
	return tea.Batch(cmds...)
}

const cronTickInterval = 30 * time.Second
const asyncHookTickInterval = 500 * time.Millisecond
const maxCronQueueSize = 100

type CronTickMsg struct{}

type AsyncHookTickMsg struct{}

func TriggerCronTickNow() tea.Cmd {
	return func() tea.Msg { return CronTickMsg{} }
}

func StartCronTicker() tea.Cmd {
	return tea.Tick(cronTickInterval, func(time.Time) tea.Msg {
		return CronTickMsg{}
	})
}

func StartAsyncHookTicker() tea.Cmd {
	return tea.Tick(asyncHookTickInterval, func(time.Time) tea.Msg {
		return AsyncHookTickMsg{}
	})
}

type CronResult struct {
	InjectPrompt string
	Notices      []string
}

func (s *Model) HandleCronTick(cronSvc *cron.Scheduler, isIdle bool) CronResult {
	var result CronResult

	if cronSvc == nil || (cronSvc.Empty() && len(s.CronQueue) == 0) {
		return result
	}

	fired := cronSvc.Tick()
	injected := false

	for i, f := range fired {
		if !isIdle || i > 0 {
			// Queue if busy, or if another cron prompt already started
			if len(s.CronQueue) < maxCronQueueSize {
				s.CronQueue = append(s.CronQueue, f.Prompt)
			}
		} else {
			result.InjectPrompt = f.Prompt
			injected = true
		}
	}

	// Drain one queued prompt if idle and no prompt was already injected this tick
	if isIdle && !injected && len(s.CronQueue) > 0 {
		result.InjectPrompt = s.CronQueue[0]
		s.CronQueue = s.CronQueue[1:]
	}

	return result
}

func (s *Model) HandleAsyncHookTick(isIdle bool) *AsyncHookRewake {
	if !isIdle {
		return nil
	}

	item, ok := s.AsyncHookQueue.Pop()
	if !ok {
		return nil
	}
	return &item
}
