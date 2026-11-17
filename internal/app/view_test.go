package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/app/kit/suggest"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestLiveFlowGraphAppearsWithTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := task.NewAgentTask("wf-1", "flow", "/flow demo", ctx, cancel, "")
	wf.MarkScheduler()
	wf.SetLiveView("Flow demo (1/3 finished)\n  ╭────╮\n  │ ●  │\n  ╰────╯")
	other := task.NewAgentTask("other", "Explore", "search", ctx, cancel, "")
	view := appendLiveFlowViews("Background", []task.BackgroundTask{other, wf}, 0, 100)
	if !strings.Contains(view, "Flow demo (1/3 finished)") || strings.Contains(view, "search") {
		t.Fatalf("live flow graph was not attached selectively:\n%s", view)
	}
}

func TestBackgroundAgentProgressStaysWithItsTrackerRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := task.NewManager()
	tracker := todo.NewStore()
	worker := mgr.CreateAgentTask("review-1", "Reviewer", "Audit", ctx, cancel)
	worker.SetLiveView("Read(main.go) failed: file missing\n" + strings.Repeat("diagnostics", 1000))
	todo.TrackWorker(tracker, worker.GetStatus())
	m := fixedComposerModel(80, 40)
	m.services.Task, m.services.Tracker = mgr, tracker
	m.conv.ShowTasks = true
	view := ansi.Strip(m.renderTrackerList())
	if !strings.Contains(view, "Audit") || !strings.Contains(view, "Read(main.go) failed: file missing") || strings.Contains(view, "diagnostics") {
		t.Fatalf("worker-local progress missing or unbounded: %q", view)
	}
	if len(m.conv.Messages) != 0 {
		t.Fatal("worker progress became a conversation message")
	}
}

func TestLiveFlowReflows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := task.NewAgentTask("wf-width", "flow", "/flow demo", ctx, cancel, "")
	wf.MarkScheduler()
	wf.SetLiveView("snapshot ●")
	wf.SetLiveViewRenderer(func(width int) string { return fmt.Sprintf("width %d ●", width) })
	running := []task.BackgroundTask{wf}
	if got := ansi.Strip(appendLiveFlowViews("", running, 0, 80)); got != "width 80 ●" {
		t.Fatalf("initial view = %q", got)
	}
	if got := ansi.Strip(appendLiveFlowViews("", running, 3, 120)); got != "width 120 ●" {
		t.Fatalf("resized view = %q", got)
	}
}

func TestLiveFlowBreathesOnlyRunningGraphNodes(t *testing.T) {
	const graph = "  Flow demo · 1/3 finished\n    ● completed → ◉ running → ○ pending"
	first := styleFlowLiveView(graph, 0)
	if first == styleFlowLiveView(graph, 3) {
		t.Fatal("running marker should breathe with the frame clock")
	}
	for frame := range 6 {
		got := styleFlowLiveView(graph, frame)
		if ansi.Strip(got) != strings.ReplaceAll(graph, "◉", "●") {
			t.Fatalf("frame %d changed graph content: %q", frame, got)
		}
		completed := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim).Render("● completed")
		pending := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDisabled).Render("○ pending")
		if !strings.Contains(got, completed) || !strings.Contains(got, pending) {
			t.Fatal("completed and pending nodes should stay static")
		}
	}
}

func TestLiveFlowEmphasizesOnlyActivePaths(t *testing.T) {
	const graph = "Flow · Demo · round 2/3 · 3/7\n[◉ writer] ─▶ [○ critic]\n╰ REVISE ×1 ━━━━━╯"
	for _, frame := range []int{0, 3} {
		got := styleFlowLiveView(graph, frame)
		active := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus).Bold(true).Faint(frame%6 < 2).Render("━━━━━")
		if !strings.Contains(got, active) || ansi.Strip(got) != strings.ReplaceAll(graph, "◉", "●") {
			t.Fatalf("frame %d lost the actual active path:\n%s", frame, got)
		}
	}
}

func TestLiveFlowLoopFollowsCurrentRound(t *testing.T) {
	b, err := flow.Parse("```mermaid\nflowchart LR\nwriter --> critic --> decision\ndecision -->|REVISE x3| writer\ndecision -->|PASS| result\n```\n## writer\nwrite\n## critic\nreview\n## decision\nroute\n## result\nfinish\n")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wf := task.NewAgentTask("loop-live", "flow", "write", ctx, cancel, "")
	wf.MarkScheduler()
	statuses := map[string]flow.Status{}
	wf.SetLiveView("loop")
	wf.SetLiveViewRenderer(func(width int) string { return b.CompactProgressView(statuses, width) })
	for _, step := range []struct{ active, progress string }{
		{"writer#1", "0/4"}, {"critic#1", "1/4"}, {"decision#1", "2/4"},
		{"writer#2", "3/7"}, {"critic#2", "4/7"}, {"decision#2", "5/7"},
	} {
		statuses[step.active] = flow.StatusRunning
		for _, width := range []int{80, 120} {
			first := appendLiveFlowViews("", []task.BackgroundTask{wf}, 0, width)
			if first == appendLiveFlowViews("", []task.BackgroundTask{wf}, 3, width) {
				t.Fatal("current loop node did not animate")
			}
			plain := ansi.Strip(first)
			if strings.Contains(plain, "#") || !strings.Contains(plain, "REVISE") || !strings.Contains(plain, "PASS") || !strings.Contains(plain, step.progress) {
				t.Fatalf("loop did not follow %s:\n%s", step.active, plain)
			}
			active, _, _ := strings.Cut(step.active, "#")
			bold := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus).Bold(true).Render(active)
			if !strings.Contains(first, bold) {
				t.Fatalf("active loop node %s was not emphasized", active)
			}
			if strings.Count(plain, "\n") > 4 {
				t.Fatalf("loop expanded into a round list:\n%s", plain)
			}
		}
		statuses[step.active] = flow.StatusSucceeded
	}
}

func TestLiveFlowReplacesItsBackgroundTrackerRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := task.NewManager()
	tracker := todo.NewStore()
	wf := mgr.CreateSchedulerTask("wf-tracker", "flow", "/flow demo", ctx, cancel)
	wf.SetLiveView("Flow demo · 0/1 finished\n  ● map")
	todo.TrackWorker(tracker, wf.GetStatus())
	m := &model{env: env{Width: 100}, services: services{Tracker: tracker, Task: mgr, Subagent: subagent.NewRegistry()}, conv: conv.NewModel(100)}
	m.conv.ShowTasks = true
	view := m.renderTrackerList()
	if strings.Contains(view, "Background") || !strings.Contains(view, "Flow demo · 0/1 finished") {
		t.Fatalf("duplicate tracker row:\n%s", view)
	}
}

// The composer prints the "❭ " prompt once, on the first row, while inputCursor
// offsets the cursor by that prompt's width on every row. hangComposerRows is
// what keeps the two in step: without the gutter, wrapped and newline-split rows
// start at column 0 and the cursor floats two columns right of the text.
func TestComposerCursorAlignsWithEveryRow(t *testing.T) {
	const width = 80

	tests := []struct {
		name  string
		value string
	}{
		{"hard newline", "first line\nsecond"},
		{"soft wrap", strings.TrimSpace(strings.Repeat("ab/cd-ef ", 12))},
		{"cjk soft wrap", strings.Repeat("看看当前分支对 readme 的更改，", 6)},
		{"single row", "short"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &model{
				env:       env{Width: width, Height: 24, Ready: true},
				conv:      conv.NewModel(width),
				userInput: input.New("", width, nil, input.SelectorDeps{}),
			}
			m.userInput.Textarea.SetWidth(width - 4 - 2)
			m.userInput.Textarea.SetValue(tt.value)
			m.userInput.Textarea.CursorEnd()

			lines := strings.Split(m.renderInputView(), "\n")
			cursor := m.inputCursor(0)
			if cursor == nil {
				t.Fatal("composer reported no cursor")
			}
			if cursor.Position.Y != len(lines)-1 {
				t.Fatalf("cursor on row %d, but the value ends on row %d", cursor.Position.Y, len(lines)-1)
			}

			// The cursor sits at the end of the value, so it must land exactly
			// where the drawn row's text stops.
			row := strings.TrimRight(ansi.Strip(lines[cursor.Position.Y]), " ")
			if got := lipgloss.Width(row); got != cursor.Position.X {
				t.Fatalf("cursor at column %d, but row %q ends at column %d", cursor.Position.X, row, got)
			}

			// Every row has to fit the terminal, gutter included, or the
			// terminal rewraps the composer under the cursor.
			for i, line := range lines {
				if w := lipgloss.Width(line); w > width {
					t.Fatalf("row %d is %d columns wide, exceeds terminal width %d", i, w, width)
				}
			}
		})
	}
}

func TestComposerStaysAtBottomAcrossLiveUpdates(t *testing.T) {
	for _, height := range []int{24, 40} {
		m := fixedComposerModel(80, height)
		for _, value := range []string{"COMPOSER", "first line\nsecond line", strings.Repeat("中文输入", 30)} {
			m.userInput.Textarea.SetValue(value)
			m.userInput.Textarea.CursorEnd()
			for _, content := range []string{"", "short reply", strings.Repeat("streaming line\n", height*2), "done"} {
				m.conv.Messages = []core.ChatMessage{{Role: core.ChatAssistant, Content: content}}
				for _, suggestions := range []bool{false, true} {
					if suggestions {
						m.userInput.Suggestions.UpdateSuggestions("/")
					} else {
						m.userInput.Suggestions.Reset()
					}
					frame, cursor := m.viewString()
					if cursor == nil || cursor.Position.Y != height-3 {
						t.Fatalf("height %d, input %q, suggestions %v: cursor = %#v, want row %d:\n%s", height, value, suggestions, cursor, height-3, ansi.Strip(frame))
					}
					if rows := rowCount(frame); rows != height {
						t.Fatalf("frame has %d rows, want %d", rows, height)
					}
				}
			}
		}
	}
}

// A running tool's status follows the conversation, empty rows below it: the
// layout has one rule, so tool state never moves the screen.
func TestPendingToolCallsFollowTheConversation(t *testing.T) {
	for _, height := range []int{24, 56} {
		m := fixedComposerModel(100, height)
		call := core.ToolCall{ID: "transfer", Name: "Bash", Input: `{"command":"monitor ` + strings.Repeat("transfer ", 50) + `","description":"RUNNING-TRANSFER"}`}
		m.conv.Messages = []core.ChatMessage{{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call}}}
		m.conv.Tool.Track([]core.ToolCall{call})
		m.conv.Tool.MarkCurrent(call.ID)
		m.conv.Stream.Active = true
		frame, cursor := m.viewString()
		plain := ansi.Strip(frame)
		start := strings.Index(plain, "RUNNING-TRANSFER")
		if start < 0 {
			t.Fatalf("live frame lost its tool status:\n%s", plain)
		}
		if row := strings.Count(plain[:start], "\n"); row > 4 {
			t.Fatalf("tool row %d should follow the conversation at the top:\n%s", row, plain)
		}
		if cursor.Position.Y != height-3 || rowCount(frame) != height {
			t.Fatalf("pending call displaced composer: cursor=%#v, rows=%d", cursor, rowCount(frame))
		}
	}
}

func fixedComposerModel(width, height int) *model {
	m := &model{
		env:       env{Width: width, Height: height, Ready: true},
		autopilot: new(atomic.Pointer[autopilotRuntime]),
		conv:      conv.NewModel(width),
		userInput: input.New("", width, func(string) []suggest.Suggestion {
			return []suggest.Suggestion{{Name: "help", Description: "Show commands"}, {Name: "history", Description: "Inspect conversation"}}
		}, input.SelectorDeps{}),
		services: services{
			Tracker: todo.NewStore(), Subagent: subagent.NewRegistry(),
			Setting: &setting.Settings{}, Hook: hook.NewEngine(nil, "", "", ""), LLM: &llm.Conn{},
		},
	}
	m.userInput.Textarea.SetWidth(width - 6)
	m.userInput.SetTerminalHeight(height)
	return m
}

func TestFirstTurnKeepsMessageRowsAcrossCommit(t *testing.T) {
	for _, height := range []int{24, 40} {
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			m := fixedComposerModel(80, height)
			attachTurnSession(t, m)
			m.services.Agent = &agent.Runtime{}
			m.welcomePending = true
			deliver := func(event core.Event) {
				conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: event})
			}
			messageRow := func(marker string) int {
				t.Helper()
				view := m.View()
				if rowCount(view.Content) != height || view.Cursor == nil || view.Cursor.Position.Y != height-3 {
					t.Fatalf("message update changed frame height or composer position: %#v", view.Cursor)
				}
				frame := ansi.Strip(view.Content)
				at := strings.Index(frame, marker)
				if at < 0 {
					t.Fatalf("missing %q in frame:\n%s", marker, frame)
				}
				return strings.Count(frame[:at], "\n")
			}
			for _, turn := range []struct{ user, reply string }{{"嗨", "你好。"}, {"再见", "下次见。"}} {
				m.conv.Append(core.ChatMessage{Role: core.ChatUser, Content: turn.user})
				userRow := messageRow(turn.user)
				deliver(sdkagent.InferenceStart{})
				if got := messageRow(turn.user); got != userRow {
					t.Errorf("inference moved %q from row %d to %d", turn.user, userRow, got)
				}
				deliver(sdkagent.InferenceUpdate{Delta: ai.Event{Type: ai.EventBlockDelta, Block: ai.Block{Type: ai.BlockText, Text: turn.reply}}})
				replyRow := messageRow(turn.reply)
				if gap := replyRow - messageRow(turn.user) - 1; gap != 1 {
					t.Errorf("user/reply gap = %d blank rows, want 1", gap)
				}
				deliver(sdkagent.InferenceEnd{Response: &ai.Response{Content: ai.TextContent(turn.reply)}})
				deliver(core.TurnEnded{Result: core.Result{StopReason: core.StopEndTurn}})
				if got := messageRow(turn.reply); got != replyRow {
					t.Errorf("settling reply moved it from row %d to %d", replyRow, got)
				}
			}
			if len(m.flush.pendingPrints) != 0 {
				t.Error("short first turn unnecessarily entered native scrollback")
			}
		})
	}
}

func TestPendingMessagesAboveTasks(t *testing.T) {
	tests := []struct {
		name      string
		queued    int
		tasks     int
		editing   bool
		height    int
		longReply bool
	}{
		{name: "pending and tasks", queued: 1, tasks: 1, height: 24},
		{name: "multiple pending", queued: 2, tasks: 1, height: 24},
		{name: "editing pending", queued: 2, tasks: 1, editing: true, height: 24},
		{name: "pending without tasks", queued: 1, height: 24},
		{name: "tasks without pending", tasks: 1, height: 24},
		{name: "short terminal", queued: 2, tasks: 1, height: 12, longReply: true},
		{name: "many tasks", queued: 2, tasks: 25, height: 24, editing: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const width = 80
			m := fixedComposerModel(width, tt.height)
			for range tt.tasks {
				m.services.Tracker.Create("Current task", "", "", nil)
			}
			pending := []string{"First pending message", "Second pending message"}
			for _, text := range pending[:tt.queued] {
				m.userInput.Queue.Enqueue(text, nil)
			}
			m.userInput.Textarea.SetValue("Draft message")
			if tt.editing {
				m.userInput.EnterQueueSelection()
			}
			if tt.longReply {
				m.conv.Messages = append(m.conv.Messages, core.ChatMessage{
					Role:    core.ChatAssistant,
					Content: strings.Repeat("Earlier output\n", 30) + "Latest output",
				})
			}

			frame, cursor := m.viewString()
			plain := ansi.Strip(frame)
			separator := strings.Repeat("─", width-1)
			aboveInput, _, ok := strings.Cut(plain, separator)
			if !ok {
				t.Fatalf("frame has no input separator:\n%s", plain)
			}
			visiblePending := tt.queued
			if tt.height < 24 {
				// Short screens may collapse the queue preview to its first row.
				visiblePending = min(visiblePending, 1)
			}
			for _, text := range pending[:visiblePending] {
				if count := strings.Count(aboveInput, text); count != 1 {
					t.Fatalf("pending message %q occurs %d times above input, want once:\n%s", text, count, plain)
				}
				if taskAt := strings.Index(aboveInput, "Tasks"); taskAt >= 0 && strings.Index(aboveInput, text) > taskAt {
					t.Fatalf("pending message appears below Tasks:\n%s", plain)
				}
			}
			if tt.height >= 24 && tt.tasks > 0 && !strings.Contains(aboveInput, "Current task") {
				t.Fatalf("frame lost the task list:\n%s", plain)
			}
			if tt.height >= 24 && tt.tasks > 0 && tt.tasks < 25 && tt.queued > 0 && !strings.Contains(aboveInput, "\n\n  Tasks") {
				t.Fatalf("pending messages and Tasks need a blank row between them:\n%s", plain)
			}
			if tt.queued > 0 && (tt.editing || visiblePending == tt.queued) {
				hint := "↑ edit"
				if tt.editing {
					hint = "enter done · ctrl+c delete"
				}
				if !strings.Contains(aboveInput, hint) {
					t.Fatalf("frame lost the pending edit hint %q:\n%s", hint, plain)
				}
			}
			lines := strings.Split(plain, "\n")
			if len(lines) > tt.height {
				t.Fatalf("frame has %d rows, exceeds height %d", len(lines), tt.height)
			}
			if cursor == nil || cursor.Position.Y >= len(lines) {
				t.Fatalf("invalid composer cursor: %v", cursor)
			}
			wantInput := conv.InputPrompt + m.userInput.Textarea.Value()
			if got := strings.TrimRight(lines[cursor.Position.Y], " "); got != wantInput {
				t.Fatalf("cursor row = %q, want composer %q", got, wantInput)
			}
		})
	}
}

func TestPendingMessagesStayAboveTasksDuringInference(t *testing.T) {
	m := fixedComposerModel(80, 40)
	m.services.Tracker.Create("Current task", "", "", nil)
	m.userInput.Queue.Enqueue("Pending sentinel", nil)
	m.conv.Stream = conv.StreamState{Active: true, Waiting: true}
	frame, _ := m.viewString()
	plain := ansi.Strip(frame)
	pendingAt := strings.Index(plain, "Pending sentinel")
	tasksAt := strings.Index(plain, "Tasks")
	waitingAt := strings.Index(plain, "On it")
	separatorAt := strings.Index(plain, strings.Repeat("─", 79))
	if pendingAt < 0 || tasksAt <= pendingAt || waitingAt <= tasksAt || separatorAt <= waitingAt {
		t.Fatalf("want pending, Tasks, On it, then separator:\n%s", plain)
	}
	lines := strings.Split(plain, "\n")
	for i, line := range lines {
		if strings.Contains(line, "On it") && (i+1 >= len(lines) || lines[i+1] != strings.Repeat("─", 79)) {
			t.Fatalf("waiting row is not directly above separator:\n%s", plain)
		}
	}
}

func TestSubmissionShowsSpinnerBeforeAgentEvents(t *testing.T) {
	m := fixedComposerModel(80, 24)
	provider := &restartStubProvider{requests: make(chan []core.Message, 1)}
	m.services.Agent = &agent.Runtime{}
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "test"}, nil); err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, m.services.Agent)
	m.env.LLMProvider = provider
	m.services.Reminder = reminder.New()
	m.agentEvolveCaps = m.selfLearnCapabilities()
	m.agentDisabledToolsSignature = m.disabledToolsSignature()
	m.agentAutoMemory = m.autoMemory()
	m.welcomePending = true
	msg, _ := m.conv.Append(core.ChatMessage{Role: core.ChatUser, Content: "hi"}).ToMessage()
	if cmd := m.SubmitToAgent(msg); cmd == nil {
		t.Fatal("submission returned no send command")
	}
	if frame := ansi.Strip(m.View().Content); !hasWaitingRow(frame) {
		t.Fatalf("submitted input has no response indicator before the agent starts:\n%s", frame)
	}
	if !m.conv.Stream.Active || !m.conv.Stream.Waiting || len(m.conv.Messages) != 1 {
		t.Fatal("waiting for the agent should mark the turn busy without adding a transcript message")
	}
	m.userInput.Textarea.SetValue("second input")
	m.handleSubmit()
	if m.userInput.Queue.Len() != 1 || len(m.conv.Messages) != 1 {
		t.Fatal("input during submission waiting should queue until the turn ends")
	}
	conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: sdkagent.InferenceStart{}})
	if m.conv.Stream.Waiting || m.conv.Stream.MessageID == "" || len(m.conv.Messages) != 2 {
		t.Fatal("first inference did not replace the waiting indicator with one assistant message")
	}
}

func TestFailedSubmissionClearsWaitingSpinner(t *testing.T) {
	m := fixedComposerModel(80, 24)
	m.conv.Append(core.ChatMessage{Role: core.ChatUser, Content: "hi"})
	m.conv.Stream = conv.StreamState{Active: true, Waiting: true}
	m.dispatch(agentSendFailedMsg{err: agent.ErrRuntimeStopped})
	frame := ansi.Strip(m.View().Content)
	if m.conv.Stream.Active || m.conv.Stream.Waiting || strings.Contains(frame, "Thinking...") ||
		!strings.Contains(frame, "Failed to send message:") {
		t.Fatalf("failed submission left a waiting spinner or hid its error:\n%s", frame)
	}
}

func TestWaitingSubmissionCanBeCancelled(t *testing.T) {
	m := fixedComposerModel(80, 24)
	m.services.Agent = &agent.Runtime{}
	m.services.Reminder = reminder.New()
	provider := &restartStubProvider{requests: make(chan []core.Message, 2)}
	started, release := make(chan struct{}), make(chan struct{})
	if err := m.services.Agent.Start(agent.BuildParams{
		Provider: provider, ModelID: "test",
		OnEvent: func(event core.Event) {
			if _, ok := event.(core.AgentStarted); ok {
				close(started)
				<-release
			}
		},
	}, nil); err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, m.services.Agent)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	<-started
	msg, _ := m.conv.Append(core.ChatMessage{Role: core.ChatUser, Content: "hi"}).ToMessage()
	cmd := m.sendToAgent(pendingMessage{Message: msg})
	m.handleStreamCancel()
	runCmd(cmd)
	close(release)
	if m.conv.Stream.Active || m.conv.Stream.Waiting || strings.Contains(ansi.Strip(m.View().Content), "Thinking...") {
		t.Fatal("cancelling a submission left its waiting spinner")
	}
	select {
	case <-provider.requests:
		t.Fatal("cancelled waiting submission still reached inference")
	case <-time.After(100 * time.Millisecond):
	}
	next, _ := m.conv.Append(core.ChatMessage{Role: core.ChatUser, Content: "next"}).ToMessage()
	runCmd(m.sendToAgent(pendingMessage{Message: next, FromUser: true}))
	select {
	case <-provider.requests:
	case <-time.After(time.Second):
		t.Fatal("cancellation prevented the next submission from running")
	}
}

func TestModeStatusOmitsPersistentHistoryHint(t *testing.T) {
	m := fixedComposerModel(120, 24)
	m.env.CurrentModel = &llm.CurrentModelInfo{ModelID: "test-model"}
	m.conv.Messages = []core.ChatMessage{{Role: core.ChatAssistant, Content: "done"}}
	for _, active := range []bool{false, true} {
		m.conv.Stream.Active = active
		status := ansi.Strip(m.renderModeStatus())
		if strings.Contains(status, "ctrl+o history") || !strings.Contains(status, "test-model") {
			t.Fatalf("stream active=%v: unexpected status %q", active, status)
		}
	}
	m.userInput.Provider.StatusMessage = "Model list refreshed"
	if status := ansi.Strip(m.renderModeStatus()); !strings.Contains(status, "Model list refreshed") {
		t.Fatalf("temporary status disappeared: %q", status)
	}
}

func addTallFlowActivity(t *testing.T, m *model) {
	t.Helper()
	var source strings.Builder
	source.WriteString("```mermaid\nflowchart LR\n")
	for i := range 24 {
		fmt.Fprintf(&source, " node%d -->|NEXT| node%d\n", i, i+1)
	}
	source.WriteString("```\n")
	for i := range 25 {
		fmt.Fprintf(&source, "\n## node%d\nWork\n", i)
	}
	w, err := flow.Parse(source.String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wf := task.NewAgentTask("flow-height", "flow", "review", ctx, cancel, "")
	wf.MarkScheduler()
	wf.SetLiveView(w.CompactProgressView(nil, m.env.Width))
	m.services.Task = task.NewManager()
	m.services.Task.RegisterTask(wf)
}

func TestActivityReservesComposerAndModalSpace(t *testing.T) {
	m := fixedComposerModel(80, 24)
	for i := 0; i < 9; i++ {
		m.services.Tracker.Create(fmt.Sprintf("TASK-%d", i), "", "", nil)
	}
	m.userInput.Textarea.SetValue(strings.TrimSuffix(strings.Repeat("draft line\n", 10), "\n"))
	m.userInput.Textarea.CursorEnd()
	frame, cursor := m.viewString()
	if rowCount(frame) != m.env.Height || cursor.Position.Y != m.env.Height-3 {
		t.Fatalf("tasks displaced multiline input: cursor=%#v\n%s", cursor, ansi.Strip(frame))
	}
	addTallFlowActivity(t, m)
	frame, cursor = m.viewString()
	if rowCount(frame) != m.env.Height || cursor.Position.Y != m.env.Height-3 {
		t.Fatalf("flow displaced multiline input: cursor=%#v\n%s", cursor, ansi.Strip(frame))
	}
	modal := dockedModalModel(t, "why this command is needed")
	addTallFlowActivity(t, modal)
	frame, _ = modal.viewString()
	if rowCount(frame) != modal.env.Height || !strings.Contains(ansi.Strip(frame), "1. Allow once") || !strings.Contains(ansi.Strip(frame), "2. Deny") {
		t.Fatalf("flow displaced approval options:\n%s", ansi.Strip(frame))
	}
}

func TestShortConversationGrowsFromTopWhileActivityStaysDocked(t *testing.T) {
	m := fixedComposerModel(80, 24)
	for _, content := range []string{"FIRST-ROW", "FIRST-ROW\n\nsecond paragraph"} {
		m.conv.Messages = []core.ChatMessage{{Role: core.ChatAssistant, Content: content}}
		frame, cursor := m.renderNormalView("separator", "TRACKER-LIVE")
		plain := ansi.Strip(frame)
		first := strings.Index(plain, "FIRST-ROW")
		if first < 0 || strings.Count(plain[:first], "\n") != 1 {
			t.Fatalf("short conversation moved from the top:\n%s", plain)
		}
		tracker := strings.Index(plain, "TRACKER-LIVE")
		if tracker < 0 || strings.Count(plain[:tracker], "\n") != cursor.Position.Y-2 {
			t.Fatalf("activity did not stay above the composer:\n%s", plain)
		}
	}
}

// The terminal the docked-modal tests render into.
const (
	modalTestWidth  = 80
	modalTestHeight = 24
)

// dockedModalModel builds a model whose approval modal is asking about the
// first of two parallel Bash calls the assistant has just explained, with that
// explanation still live (not yet committed to scrollback). The batch is what
// makes the fixture realistic: both calls get a PreTool event, so the "current"
// call is the *last* one tracked, not the one the modal is asking about.
// hasWaitingRow reports the fixed normal waiting copy, with or without elapsed time.
func hasWaitingRow(view string) bool {
	return slices.ContainsFunc(strings.Split(ansi.Strip(view), "\n"), func(line string) bool {
		return strings.HasPrefix(strings.TrimSpace(line), "✦ On it")
	})
}

// rowAcrossFrames returns the styled row containing marker, rendered two frame
// ticks apart: a frozen row draws the same both times, a live one does not.
func rowAcrossFrames(t *testing.T, m *model, marker string) (before, after string) {
	t.Helper()
	row := func() string {
		frame, _ := m.viewString()
		for line := range strings.SplitSeq(frame, "\n") {
			if strings.Contains(ansi.Strip(line), marker) {
				return line
			}
		}
		t.Fatalf("frame has no row containing %q:\n%s", marker, frame)
		return ""
	}
	before = row()
	for range 2 {
		m.conv.Spinner, _ = m.conv.Spinner.Update(m.conv.Spinner.Tick())
	}
	return before, row()
}

func dockedModalModel(t *testing.T, rationale string) *model {
	t.Helper()

	return dockedModalModelWithCalls(t, rationale, []core.ToolCall{
		{ID: "bash-1", Name: "Bash", Input: `{"command":"df -h","description":"check disk"}`},
		{ID: "bash-2", Name: "Bash", Input: `{"command":"date","description":"check clock"}`},
	})
}

func dockedModalModelWithCalls(t *testing.T, rationale string, batch []core.ToolCall) *model {
	t.Helper()

	const width, height = modalTestWidth, modalTestHeight
	m := &model{
		env:       env{Width: width, Height: height, Ready: true},
		conv:      conv.NewModel(width),
		userInput: input.New("", width, nil, input.SelectorDeps{}),
		services:  services{Tracker: todo.NewStore(), Subagent: subagent.NewRegistry(), Setting: &setting.Settings{}},
	}
	// The stream is still open across a permission gate — it clears only on a
	// text-only final chunk — so the live tail believes it is mid-turn.
	m.conv.Stream.Active = true
	m.conv.Messages = append(m.conv.Messages, core.ChatMessage{
		Role:      core.ChatAssistant,
		Content:   rationale,
		ToolCalls: batch,
	})
	// PreTool fires before the permission request, so both calls are already
	// tracked as in flight by the time the modal goes up.
	m.conv.Tool.Track(batch)
	for _, call := range batch {
		m.conv.Tool.MarkCurrent(call.ID)
		m.conv.Tool.MarkStarted(call.ID)
	}
	// What HandlePermGate records when the request lands: the modal is asking
	// about the first call, not the batch.
	m.conv.Tool.MarkAwaitingApproval(batch[0].ID)

	m.userInput.Approval.Show(&perm.PermissionRequest{
		ID:          "req-1",
		ToolName:    "Bash",
		Description: "check disk",
		BashMeta:    &perm.BashMetadata{Command: "df -h", Description: "check disk"},
	}, width, height)

	return m
}

// The text streamed right before a tool call is the rationale for the
// permission being requested, so the approval modal has to render on top of it
// rather than in place of it — otherwise the reasoning disappears at exactly
// the moment the user has to act on it (issue #436).
func TestDockedModalKeepsPrecedingAssistantText(t *testing.T) {
	const rationale = "RATIONALE_SENTINEL: this is not a timeout, it is a kernel-level failure"

	m := dockedModalModel(t, rationale)
	frame, _ := m.viewString()

	plain := ansi.Strip(frame)
	if !strings.Contains(plain, "RATIONALE_SENTINEL") {
		t.Fatalf("modal frame dropped the assistant text preceding the tool call:\n%s", frame)
	}
	if !strings.Contains(plain, "df -h") {
		t.Fatalf("modal frame is missing the approval prompt itself:\n%s", frame)
	}
}

func TestDockedModalHidesPendingPreview(t *testing.T) {
	m := dockedModalModel(t, "about to check the disk")
	m.userInput.Queue.Enqueue("Pending message sentinel", nil)

	frame, cursor := m.viewString()
	plain := ansi.Strip(frame)
	if strings.Contains(plain, "Pending message sentinel") || strings.Contains(plain, "↑ edit") {
		t.Fatalf("modal frame offers editing for hidden composer:\n%s", plain)
	}
	if cursor != nil {
		t.Fatal("docked modal must not show the composer cursor")
	}
}

// The chat tail above a docked modal is capped: rendering it in full would push
// the modal's answer options off the bottom of the screen, which is worse than
// showing no text at all.
func TestDockedModalStaysWithinTerminalHeight(t *testing.T) {
	rationale := strings.TrimSpace(strings.Repeat("wall of reasoning text that wraps and wraps ", 60))
	m := dockedModalModel(t, rationale)
	frame, _ := m.viewString()

	if rows := strings.Count(frame, "\n") + 1; rows > modalTestHeight {
		t.Fatalf("modal frame is %d rows tall, exceeds terminal height %d", rows, modalTestHeight)
	}
	// The options are the last thing in the modal: if they survived the cap,
	// nothing below the chat tail was pushed off screen.
	if !strings.Contains(ansi.Strip(frame), "1. Allow once") || !strings.Contains(ansi.Strip(frame), "2. Deny") {
		t.Fatalf("modal frame lost its question to the chat tail:\n%s", frame)
	}
}

// PreTool stamps a call before the permission request, so the in-flight state
// is live while the user decides. The call the modal is asking about has not
// been allowed to start: its row must say so rather than spin and tick an
// elapsed timer over the user's deliberation (issue #440).
func TestDockedModalDoesNotShowTheGatedCallAsRunning(t *testing.T) {
	m := dockedModalModel(t, "about to check the disk")

	row, later := rowAcrossFrames(t, m, "Bash(df -h)")
	if row != later {
		t.Fatalf("tool row breathes while it waits on the approval modal: %q then %q", row, later)
	}
	row = ansi.Strip(row)
	if !strings.Contains(row, "waiting for approval") {
		t.Fatalf("tool row %q does not say what it is waiting for", row)
	}
}

// The awaiting state is keyed by tool call, so the request has to carry the ID
// of the call it gates — the whole point of routing it through the modal.
func TestHandlePermGateMarksTheCallItAsksAbout(t *testing.T) {
	m := dockedModalModel(t, "about to check the disk")
	m.services.Agent = &agent.Runtime{}
	m.services.Session = nil
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.userInput.Approval.Hide()
	m.conv.Tool.ClearAwaitingApproval()

	m.HandlePermGate(&agent.PermGateRequest{
		RequestID:   "req-1",
		ToolCallID:  "bash-1",
		ToolName:    "Bash",
		Description: "check disk",
		Input:       map[string]any{"command": "df -h"},
	})

	if got := m.conv.Tool.AwaitingApprovalID; got != "bash-1" {
		t.Fatalf("awaiting call = %q, want the call the request gates", got)
	}
}

// A request with no call ID names nothing, so the per-call state has to stay
// empty rather than hold a stale ID — that is what puts the rows back on the
// docked-modal freeze instead of leaving the gated call spinning over the
// user's deliberation. Unreachable on the main agent path today, but it is the
// seam that would degrade first.
func TestHandlePermGateWithoutCallIDFallsBackToTheModalFreeze(t *testing.T) {
	m := dockedModalModel(t, "about to check the disk")
	m.services.Agent = &agent.Runtime{}
	m.services.Session = nil
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.userInput.Approval.Hide()

	m.HandlePermGate(&agent.PermGateRequest{
		RequestID:   "req-1",
		ToolName:    "Bash",
		Description: "check disk",
		Input:       map[string]any{"command": "df -h"},
	})

	if got := m.conv.Tool.AwaitingApprovalID; got != "" {
		t.Fatalf("awaiting call = %q, want no call named when the request carries no ID", got)
	}

	if row, later := rowAcrossFrames(t, m, "Bash("); row != later {
		t.Fatalf("tool row breathes while the modal owns the screen: %q then %q", row, later)
	}
}

// The stream stays open across a permission gate, so the assistant message
// carrying the rationale still counts as the live tail. Its bullet must not
// spin either — the turn is parked on the modal, not producing text.
func TestDockedModalFreezesAssistantBullet(t *testing.T) {
	m := dockedModalModel(t, "RATIONALE_SENTINEL about to check the disk")

	if row, later := rowAcrossFrames(t, m, "RATIONALE_SENTINEL"); row != later {
		t.Fatalf("assistant row breathes while the turn waits on the modal: %q then %q", row, later)
	}
}

// An Agent row is drawn by its own branch, which blinks its icon off the frame
// counter instead of using the shared spinner and offers "(ctrl+o to expand)".
// Both are wrong under a modal — the row is not working, and the modal owns
// ctrl+o. (The glyph freeze itself is pinned in conv; this covers the wiring.)
func TestDockedModalDropsDeadExpandHint(t *testing.T) {
	m := dockedModalModelWithCalls(t, "spawning a reviewer", []core.ToolCall{
		{ID: "agent-1", Name: tool.ToolAgent, Input: `{"agent":"reviewer","prompt":"review the diff"}`},
	})

	frame, _ := m.viewString()

	if strings.Contains(ansi.Strip(frame), "ctrl+o") {
		t.Fatalf("modal frame offers ctrl+o while the modal owns the keyboard:\n%s", frame)
	}
}

// A full-screen picker takes the alternate screen; a docked modal stays
// inline, where the composer sat, under the conversation it asks about.
func TestOnlyFullScreenPanelsUseAlternateScreen(t *testing.T) {
	m := dockedModalModel(t, "about to inspect the repository")
	if view := m.View(); view.AltScreen {
		t.Fatal("docked approval modal must render inline, where the composer sits")
	}
	m.userInput.Approval.Hide()
	m.userInput.Settings.Enter(m.env.Width, m.env.Height)
	if view := m.View(); !view.AltScreen {
		t.Fatal("fullscreen picker must render in the alternate screen")
	}
}

func TestTailLines(t *testing.T) {
	five := "L0\nL1\nL2\nL3\nL4"

	tests := []struct {
		name     string
		in       string
		maxLines int
		want     string
	}{
		{"no room drops content", five, 0, ""},
		{"negative room drops content", five, -3, ""},
		{"fewer lines than max returns input", "a\nb", 5, "a\nb"},
		{"exact fit returns input", five, 5, five},
		{"truncates to last N (latest)", five, 2, "L3\nL4"},
		{"single line cap keeps latest", five, 1, "L4"},
		{"empty string", "", 3, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tailLines(tt.in, tt.maxLines)
			if got != tt.want {
				t.Fatalf("tailLines(%q, %d) = %q, want %q", tt.in, tt.maxLines, got, tt.want)
			}
			// The result must never exceed maxLines rows when capping applies.
			if tt.maxLines > 0 {
				if n := strings.Count(got, "\n") + 1; n > tt.maxLines && got != tt.in {
					t.Fatalf("tailLines(%q, %d) returned %d lines, exceeds cap", tt.in, tt.maxLines, n)
				}
			}
		})
	}
}
