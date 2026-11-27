package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/runtime/reminder"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestInferencePlacementImmediatelyAboveComposer(t *testing.T) {
	for _, width := range []int{100, 40, 32} {
		for _, height := range []int{8, 12} {
			for _, pending := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/pending=%v", width, height, pending), func(t *testing.T) {
					m := modelWithInferenceProgress(height)
					m.env.Width = width
					m.conv.Stream.StartedAt = time.Now().Add(-17 * time.Second)
					tracker := ""
					if pending {
						tracker = "Tasks · background"
						m.userInput.Queue.Enqueue("queued-message", nil)
					}
					frame, cursor := m.renderNormalView(strings.Repeat("─", width-1), tracker, "")
					plain := ansi.Strip(frame)
					lines := strings.Split(plain, "\n")
					status := -1
					for i, line := range lines {
						if strings.Contains(line, "✦") {
							status = i
						}
					}
					if pending {
						if strings.Contains(plain, "✦") {
							t.Fatalf("queue should replace routine wait feedback:\n%s", plain)
						}
					} else if strings.Count(plain, "✦") != 1 || status != height-5 || !strings.HasPrefix(lines[status], "✦ On it · ") || lines[status+1] != strings.Repeat("─", width-1) || ansi.StringWidth(lines[status]) >= width {
						t.Fatalf("expected inference on its own row above the composer divider:\n%s", plain)
					}
					if pending && height == 12 && (!strings.Contains(plain, "Tasks") || !strings.Contains(plain, "queued-message")) {
						t.Fatalf("missing task/queue blocks:\n%s", plain)
					}
					if len(lines) != height || cursor == nil || cursor.Position.Y != height-3 {
						t.Fatalf("composer displaced: rows=%d cursor=%+v\n%s", len(lines), cursor, plain)
					}
				})
			}
		}
	}
}

func TestForegroundWaitStaysInFooterBetweenSteps(t *testing.T) {
	m := fixedComposerModel(80, 12)
	call := core.ToolCall{ID: "tool-1", Name: "Bash", Input: `{"command":"echo done"}`}
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call}})
	m.conv.Tool.Track([]core.ToolCall{call})
	m.conv.Stream = chat.StreamState{Active: true, StartedAt: time.Now().Add(-5 * time.Second)}
	if frame := ansi.Strip(m.View().Content); !strings.Contains(frame, "echo done") || strings.Contains(frame, "✦ On it") {
		t.Fatalf("running tool should own its activity:\n%s", frame)
	}
	m.conv.Tool.MarkComplete(call.ID)
	m.conv.Append(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: call.ID, ToolName: call.Name}})
	m.conv.CommittedCount = len(m.conv.Messages)
	for _, inferring := range []bool{false, true} {
		m.conv.Stream.Inferring = inferring
		view := m.View()
		frame := ansi.Strip(view.Content)
		rows := strings.Split(frame, "\n")
		if view.Cursor == nil || view.Cursor.Position.Y != 9 || strings.Count(frame, "✦ On it") != 1 ||
			!strings.HasPrefix(rows[7], "✦ On it · ") || rows[8] != strings.Repeat("─", 79) {
			t.Fatalf("inferring=%v: waiting status lost its footer row:\n%s", inferring, frame)
		}
	}
	m.conv.Stream.Inferring = false
	m.userInput.Queue.Enqueue("queued follow-up", nil)
	if frame := ansi.Strip(m.View().Content); !strings.Contains(frame, "queued follow-up") || strings.Contains(frame, "✦ On it") {
		t.Fatalf("routine wait competed with the queue:\n%s", frame)
	}
}

func TestTaskTrackerStaysPutAsForegroundWaitChanges(t *testing.T) {
	for _, width := range []int{24, 80} {
		for _, height := range []int{8, 24} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m := fixedComposerModel(width, height)
				m.conv.ShowTasks = true
				m.services.Tracker.Create("Current task", "", "", nil)
				m.userInput.Queue.Enqueue("queued prompt", nil)
				started := time.Now().Add(-7 * time.Second)
				var initial []string
				var bottomRows int
				for _, state := range []struct {
					name   string
					stream chat.StreamState
				}{
					{name: "idle"},
					{"submitted", chat.StreamState{Active: true, Waiting: true, StartedAt: started}},
					{"inferring", chat.StreamState{Active: true, Inferring: true, StartedAt: started}},
					{name: "cleared"},
				} {
					m.conv.Stream = state.stream
					view := m.View()
					plain := ansi.Strip(view.Content)
					rows := strings.Split(plain, "\n")
					if len(rows) != height || view.Cursor == nil || view.Cursor.Position.Y != height-3 {
						t.Fatalf("%s: composer moved:\n%s", state.name, plain)
					}
					if rows[view.Cursor.Position.Y-1] != strings.Repeat("─", width-1) || strings.Contains(plain, "✦") {
						t.Fatalf("%s: routine wait should not compete with Tasks and the queue:\n%s", state.name, plain)
					}
					bottom, _ := m.frameBottom()
					if initial == nil {
						if !strings.Contains(plain, "Tasks") || !strings.Contains(plain, "Current task") {
							t.Fatalf("fixture has no visible task block:\n%s", plain)
						}
						initial, bottomRows = rows, rowCount(bottom)
						continue
					}
					for i, row := range rows {
						if row != initial[i] {
							t.Fatalf("%s: row %d outside the rule changed from %q to %q", state.name, i, initial[i], row)
						}
					}
					if rowCount(bottom) != bottomRows {
						t.Fatalf("%s: scrollback budget changed", state.name)
					}
				}
			})
		}
	}
}

func TestForegroundWaitWithRunningTask(t *testing.T) {
	const width, height = 64, 12
	m := fixedComposerModel(width, height)
	m.services.Task = task.NewSupervisor()
	worker := viewerWorker(m, "coverage", "", time.Now().Add(-41*time.Second))
	worker.info.Description = "Measure targeted Go coverage"
	todo.TrackWorker(m.services.Tracker, worker.info)
	started := time.Now().Add(-7 * time.Second)
	m.conv.Stream = chat.StreamState{Active: true, Inferring: true, StartedAt: started}

	for _, tc := range []struct {
		name, status string
		showTasks    bool
		queued       bool
		attempt      int
		lastDataAt   time.Time
	}{
		{"Running task and queued prompt", "", true, true, 1, started},
		{"Queued prompt only", "", false, true, 1, started},
		{"Running task only", "", true, false, 1, started},
		{"No other live indicators", "✦ On it", false, false, 1, started},
		{"Retrying with a running task", "✦ Retrying", true, true, 2, started},
		{"Quiet model with a running task", "", true, false, 1, time.Now().Add(-time.Minute)},
		{"Quiet model with a running task and queued prompt", "", true, true, 1, time.Now().Add(-time.Minute)},
		{"Quiet model without task indicators", "✦ Waiting for model", false, false, 1, time.Now().Add(-time.Minute)},
		{"Quiet model with a queued prompt only", "✦ Waiting for model", false, true, 1, time.Now().Add(-time.Minute)},
	} {
		m.userInput.Queue.Clear()
		if tc.queued {
			m.userInput.Queue.Enqueue("Show me the summary", nil)
		}
		m.conv.ShowTasks, m.conv.Stream.Attempt, m.conv.Stream.LastDataAt = tc.showTasks, tc.attempt, tc.lastDataAt
		view := m.View()
		plain := ansi.Strip(view.Content)
		rows := strings.Split(plain, "\n")
		if len(rows) != height || view.Cursor == nil || view.Cursor.Position.Y != height-3 {
			t.Fatalf("%s moved the composer:\n%s", tc.name, plain)
		}
		queueAt := strings.Index(plain, "Show me the summary")
		taskAt := strings.Index(plain, "● Measure targeted Go coverage")
		if (queueAt >= 0) != tc.queued || (taskAt >= 0) != tc.showTasks || tc.queued && tc.showTasks && taskAt <= queueAt {
			t.Fatalf("%s: pending message or task out of place:\n%s", tc.name, plain)
		}
		rule := view.Cursor.Position.Y - 1
		if rows[rule] != strings.Repeat("─", width-1) {
			t.Fatalf("%s: composer divider should remain plain:\n%s", tc.name, plain)
		}
		if tc.status == "" {
			if strings.Contains(plain, "✦") {
				t.Fatalf("%s: routine wait should be hidden:\n%s", tc.name, plain)
			}
		} else if !strings.HasPrefix(rows[rule-1], tc.status) || strings.Count(plain, "✦") != 1 {
			t.Fatalf("%s: expected one status row above the composer divider:\n%s", tc.name, plain)
		}
		t.Logf("%s:\n%s", tc.name, strings.TrimLeft(plain, "\n"))
	}
}

func TestInferencePlacementModalSuppressesStandaloneRow(t *testing.T) {
	for _, width := range []int{100, 40, 32} {
		for _, height := range []int{8, 12} {
			for _, question := range []bool{false, true} {
				m := modelWithInferenceProgress(height)
				m.env.Width = width
				if question {
					m.conv.Modal.Question.Show(&tool.QuestionRequest{Questions: []tool.Question{{Question: "Continue?", Options: []tool.QuestionOption{{Label: "Yes"}}}}}, width)
				} else {
					m.userInput.Approval.Show(&perm.PermissionRequest{ID: "placement", ToolName: "Bash", Description: "check"}, width, height)
				}
				if frame := ansi.Strip(m.View().Content); strings.Contains(frame, "✦") {
					t.Fatalf("%dx%d question=%v duplicated inference under modal:\n%s", width, height, question, frame)
				}
			}
		}
	}
}

func TestCompactionPlacementAndRecovery(t *testing.T) {
	for _, width := range []int{32, 100} {
		m := modelWithInferenceProgress(12)
		m.env.Width = width
		m.OnCompactStart(83)
		m.userInput.Queue.Enqueue("queued-message", nil)
		frame, cursor := m.renderNormalView(strings.Repeat("─", width-1), "Tasks · background", "")
		plain := ansi.Strip(frame)
		lines := strings.Split(plain, "\n")
		const status = "✦ Summarizing conversation"
		if strings.Count(plain, status) != 1 || strings.Count(plain, "✦") != 1 {
			t.Fatalf("compaction must own one status row:\n%s", plain)
		}
		for i, line := range lines {
			if strings.Contains(line, status) && (!strings.HasPrefix(line, status) || cursor == nil || i != cursor.Position.Y-2 || lines[i+1] != strings.Repeat("─", width-1)) {
				t.Fatalf("compaction not above the composer divider:\n%s", plain)
			}
		}
		m.conv.Modal.Question.Show(&tool.QuestionRequest{Questions: []tool.Question{{Question: "Continue?"}}}, width)
		if frame := ansi.Strip(m.View().Content); strings.Contains(frame, "Summarizing conversation") {
			t.Fatalf("compaction competes with modal:\n%s", frame)
		}
		m.OnCompactEnd(sdkagent.CompactionEnd{})
		if m.conv.Compact.Active || m.conv.Compact.AutoContinue || !m.conv.Compact.StartedAt.IsZero() {
			t.Fatal("compaction left stale recovery status")
		}
	}
}

func TestCompactionCompletionIsTopLevel(t *testing.T) {
	m := modelWithInferenceProgress(24)
	m.services.Reminder = &reminder.Queue{}
	m.env.CWD = t.TempDir()
	m.OnCompactStart(83)
	m.OnCompacted(core.Compacted{Summary: "private summary body", OriginalCount: 83})

	const notice = "≡ Conversation compacted — 83 messages summarized (scroll up for history)"
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "Continued reply"})
	for _, committed := range []bool{false, true} {
		if committed {
			m.CommitMessages()
		}
		frame := ansi.Strip(m.View().Content)
		at, before := strings.Index(frame, "\n"+notice+"\n"), strings.Index(frame, "I will write the HTML now.")
		after := strings.Index(frame, "Continued reply")
		if before < 0 || at <= before || after <= at || strings.Count(frame, notice) != 1 ||
			strings.Contains(frame, "Summarizing conversation") || strings.Contains(frame, "private summary body") {
			t.Fatalf("committed=%v: expected one top-level boundary between replies:\n%s", committed, frame)
		}
	}
}

func TestInferencePlacementRemovedOnCompletion(t *testing.T) {
	m := modelWithInferenceProgress(12)
	chat.Update(m, &m.conv, chat.AgentOutboxMsg{Event: sdkagent.InferenceEnd{Attempt: 1, Response: &ai.Response{Content: ai.TextContent("done")}}})
	if frame := ansi.Strip(m.View().Content); strings.Contains(frame, "✦") {
		t.Fatalf("completed inference retained status:\n%s", frame)
	}
}
