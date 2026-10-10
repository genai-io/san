package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit/suggest"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/workflow"
)

func TestLiveWorkflowGraphAppearsWithTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := task.NewAgentTask("wf-1", "workflow", "/workflow demo", ctx, cancel, "")
	wf.SetLiveView("Workflow demo (1/3 finished)\n  ╭────╮\n  │ ●  │\n  ╰────╯")
	other := task.NewAgentTask("other", "Explore", "search", ctx, cancel, "")
	view := appendLiveWorkflowViews("Background", []task.BackgroundTask{other, wf}, 0, 100)
	if !strings.Contains(view, "Workflow demo (1/3 finished)") || strings.Contains(view, "search") {
		t.Fatalf("live workflow graph was not attached selectively:\n%s", view)
	}
}

func TestLiveWorkflowReflowsAndPulses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := task.NewAgentTask("wf-width", "workflow", "/workflow demo", ctx, cancel, "")
	wf.SetLiveView("snapshot ●")
	wf.SetLiveViewRenderer(func(width int) string { return fmt.Sprintf("width %d ●", width) })
	running := []task.BackgroundTask{wf}
	if got := ansi.Strip(appendLiveWorkflowViews("", running, 0, 80)); got != "width 80 ●" {
		t.Fatalf("initial view = %q", got)
	}
	if got := ansi.Strip(appendLiveWorkflowViews("", running, 3, 120)); got != "width 120 ◉" {
		t.Fatalf("resized pulse = %q", got)
	}
	if got := ansi.Strip(appendLiveWorkflowViews("", running, 6, 120)); got != "width 120 ●" {
		t.Fatalf("pulse did not return to solid = %q", got)
	}
}

func TestLiveWorkflowSpinsOnlyActivityAndKeepsFinishedMarkersFixed(t *testing.T) {
	activity := "  Activity\n    │  ● running\n    │  ✓ done\n    │  ✗ failed"
	graph := "  Workflow demo · 0/1 finished\n    map ● → next ○"
	input := activity + "\n\n" + graph
	for frame, marker := range []string{"|", "/", "-", "\\", "|"} {
		got := styleWorkflowLiveView(input, frame)
		graphFrame := graph
		if (frame/3)%2 == 1 {
			graphFrame = strings.Replace(graph, "●", "◉", 1)
		}
		want := strings.Replace(activity, "● running", marker+" running", 1) + "\n\n" + graphFrame
		if ansi.Strip(got) != want {
			t.Fatalf("frame %d: got %q, want %q", frame, got, want)
		}
		if !strings.Contains(got, activityDoneStyle.Render("✓")) || !strings.Contains(got, activityFailedStyle.Render("✗")) {
			t.Fatalf("frame %d: finished markers lost their styles: %q", frame, got)
		}
	}
}

func TestLiveWorkflowActivityPreservesToolContent(t *testing.T) {
	const activity = "  Activity\n    ╭─ map\n" +
		"    │  ● Bash(printf '● ✓ ✗')\n" +
		"    │  ✓ Bash(printf '●')\n" +
		"    │    └ ● ✓ ✗\n" +
		"    │      ● wrapped result\n    ╰─"
	for frame, marker := range []string{"|", "/", "-", "\\"} {
		got := ansi.Strip(styleWorkflowActivity(activity, frame))
		want := strings.Replace(activity, "│  ● Bash", "│  "+marker+" Bash", 1)
		if got != want {
			t.Fatalf("frame %d changed tool content:\n%s\nwant:\n%s", frame, got, want)
		}
	}
}

func TestLiveWorkflowReplacesItsBackgroundTrackerRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := task.NewManager()
	tracker := todo.NewStore()
	wf := mgr.CreateAgentTask("wf-tracker", "workflow", "/workflow demo", ctx, cancel)
	wf.SetLiveView("Workflow demo · 0/1 finished\n  map ●")
	todo.TrackWorker(tracker, wf.GetStatus())
	m := &model{env: env{Width: 100}, services: services{Tracker: tracker, Task: mgr, Subagent: subagent.NewRegistry()}, conv: conv.NewModel(100)}
	m.conv.ShowTasks = true
	view := m.renderTrackerList()
	if strings.Contains(view, "Background") || !strings.Contains(view, "Workflow demo · 0/1 finished") {
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

func fixedComposerModel(width, height int) *model {
	m := &model{
		env:  env{Width: width, Height: height, Ready: true},
		conv: conv.NewModel(width),
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

func addTallWorkflowActivity(t *testing.T, m *model) {
	t.Helper()
	w, err := workflow.Parse("```mermaid\nflowchart LR\n plan --> review --> merge\n```\n\n## plan\nPlan\n\n## review\nfor_each: plan.tasks\nmax_workers: 4\n\nReview {{item.prompt}}\n\n## merge\nMerge\n")
	if err != nil {
		t.Fatal(err)
	}
	var events []workflow.ActivityEvent
	for i := 0; i < 8; i++ {
		events = append(events, workflow.ActivityEvent{
			Node: "review", Worker: fmt.Sprintf("worker-%d", i),
			Text: "Read(file.go)", ToolID: fmt.Sprintf("tool-%d", i),
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wf := task.NewAgentTask("workflow-height", "workflow", "review", ctx, cancel, "")
	wf.SetLiveView(w.ActivityStreamView(events, m.env.Width) + "\n\n" + w.CompactProgressView(nil, m.env.Width))
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
	addTallWorkflowActivity(t, m)
	frame, cursor = m.viewString()
	if rowCount(frame) != m.env.Height || cursor.Position.Y != m.env.Height-3 {
		t.Fatalf("workflow displaced multiline input: cursor=%#v\n%s", cursor, ansi.Strip(frame))
	}
	modal := dockedModalModel(t, "why this command is needed")
	addTallWorkflowActivity(t, modal)
	frame, _ = modal.viewString()
	if rowCount(frame) != modal.env.Height || !strings.Contains(ansi.Strip(frame), "Do you want to proceed?") {
		t.Fatalf("workflow displaced approval options:\n%s", ansi.Strip(frame))
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
	if !strings.Contains(ansi.Strip(frame), "Do you want to proceed?") {
		t.Fatalf("modal frame lost its question to the chat tail:\n%s", frame)
	}
}

// PreTool stamps a call before the permission request, so the in-flight state
// is live while the user decides. The call the modal is asking about has not
// been allowed to start: its row must say so rather than spin and tick an
// elapsed timer over the user's deliberation (issue #440).
func TestDockedModalDoesNotShowTheGatedCallAsRunning(t *testing.T) {
	m := dockedModalModel(t, "about to check the disk")
	spinnerGlyph := ansi.Strip(m.conv.Spinner.View())

	frame, _ := m.viewString()

	var row string
	for line := range strings.SplitSeq(ansi.Strip(frame), "\n") {
		if strings.Contains(line, "Bash(df -h)") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("modal frame has no row for the gated call:\n%s", frame)
	}
	if strings.Contains(row, spinnerGlyph) {
		t.Fatalf("tool row %q spins while it waits on the approval modal", row)
	}
	if !strings.Contains(row, "waiting for approval") {
		t.Fatalf("tool row %q does not say what it is waiting for", row)
	}
}

// The awaiting state is keyed by tool call, so the request has to carry the ID
// of the call it gates — the whole point of routing it through the modal.
func TestHandlePermGateMarksTheCallItAsksAbout(t *testing.T) {
	m := dockedModalModel(t, "about to check the disk")
	m.services.Agent = &agent.Session{}
	m.services.Session = &session.Setup{}
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
	m.services.Agent = &agent.Session{}
	m.services.Session = &session.Setup{}
	spinnerGlyph := ansi.Strip(m.conv.Spinner.View())

	m.HandlePermGate(&agent.PermGateRequest{
		RequestID:   "req-1",
		ToolName:    "Bash",
		Description: "check disk",
		Input:       map[string]any{"command": "df -h"},
	})

	if got := m.conv.Tool.AwaitingApprovalID; got != "" {
		t.Fatalf("awaiting call = %q, want no call named when the request carries no ID", got)
	}

	frame, _ := m.viewString()
	for line := range strings.SplitSeq(ansi.Strip(frame), "\n") {
		if !strings.Contains(line, "Bash(") {
			continue
		}
		if strings.Contains(line, spinnerGlyph) {
			t.Fatalf("tool row %q spins while the modal owns the screen", line)
		}
	}
}

// The stream stays open across a permission gate, so the assistant message
// carrying the rationale still counts as the live tail. Its bullet must not
// spin either — the turn is parked on the modal, not producing text.
func TestDockedModalFreezesAssistantBullet(t *testing.T) {
	m := dockedModalModel(t, "RATIONALE_SENTINEL about to check the disk")
	spinnerGlyph := ansi.Strip(m.conv.Spinner.View())

	frame, _ := m.viewString()

	for line := range strings.SplitSeq(ansi.Strip(frame), "\n") {
		if strings.Contains(line, "RATIONALE_SENTINEL") && strings.Contains(line, spinnerGlyph) {
			t.Fatalf("assistant row %q spins while the turn waits on the modal", line)
		}
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
