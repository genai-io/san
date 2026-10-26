package app

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
)

func TestTaskNoticeCarriesFailureEvidenceAndAttempt(t *testing.T) {
	msg, ok := taskCompletionMessage(task.TaskInfo{
		ID: "provider", Type: task.TaskTypeBash, Description: "TUI provider", Command: "long private command", Status: task.StatusFailed,
		Attempt: 2, ExitCode: 137, Error: "command timed out after 2m0s", Lifetime: task.LifetimeJob,
		Failure: task.FailureInfo{Kind: task.FailureTimeout, Signal: "killed", Timeout: 2 * time.Minute},
	})
	if !ok || !strings.HasPrefix(msg.Subject, "TUI provider failed") || strings.Contains(msg.Subject, "private command") {
		t.Fatalf("notice used command instead of task identity: %+v", msg)
	}
	var decoded struct {
		Attempt int    `xml:"attempt,attr"`
		Kind    string `xml:"failure-kind,attr"`
		Code    int    `xml:"exit-code,attr"`
		Timeout int    `xml:"timeout-ms,attr"`
	}
	if err := xml.Unmarshal([]byte(msg.Content), &decoded); err != nil || decoded.Attempt != 2 || decoded.Kind != "timeout" || decoded.Code != 137 || decoded.Timeout != 120000 {
		t.Fatalf("lost runtime evidence: %+v, %v", decoded, err)
	}
	if n := fromBrokerMessage(msg); n.FinishedTaskID != "provider" || n.FinishedAttempt != 2 {
		t.Fatalf("lost attempt routing: %+v", n)
	}
}

func TestTaskCompletionVisibilityIsPerAttempt(t *testing.T) {
	m := interactionModel()
	worker := viewerWorker(m, "stable-task", "", time.Now())
	worker.info.Status, worker.info.Attempt = task.StatusCompleted, 1
	msg, _ := taskCompletionMessage(worker.info)
	m.showNoticeLine(fromBrokerMessage(msg))
	if !m.taskCompletionShown(worker.GetID()) {
		t.Fatal("first attempt was not acknowledged as displayed")
	}
	worker.info.Attempt = 2
	if m.taskCompletionShown(worker.GetID()) {
		t.Fatal("old notice hid the next attempt before it was displayed")
	}
	m.showNoticeLine(fromBrokerMessage(msg))
	if m.taskCompletionShown(worker.GetID()) {
		t.Fatal("delayed first-attempt notice acknowledged the new execution")
	}
	msg, _ = taskCompletionMessage(worker.info)
	m.showNoticeLine(fromBrokerMessage(msg))
	if !m.taskCompletionShown(worker.GetID()) {
		t.Fatal("new attempt completion was not acknowledged")
	}
}

func TestObsoleteTaskCompletionDoesNotWakeMain(t *testing.T) {
	for _, state := range []struct {
		name         string
		active, held bool
	}{
		{"idle", false, false}, {"streaming", true, false},
		{"held idle", false, true}, {"held streaming", true, true},
	} {
		t.Run(state.name, func(t *testing.T) {
			m, provider := noticeDeliveryModel(t)
			m.services.Task = task.NewManager()
			worker := viewerWorker(m, "continued", "", time.Now())
			worker.info.Status, worker.info.Attempt = task.StatusFailed, 1
			worker.info.Error, worker.info.AgentSessionID = "obsolete attempt failed", "child"
			message, _ := taskCompletionMessage(worker.info)
			worker.info.Status, worker.info.Attempt = task.StatusRunning, 2
			m.conv.Stream.Active, m.backgroundNoticesHeld = state.active, state.held

			if cmd := m.deliverNotice(fromBrokerMessage(message)); cmd != nil {
				t.Fatal("obsolete completion scheduled work")
			}
			if len(m.conv.Messages) != 0 || len(m.pendingMessages) != 0 || m.conv.Stream.Active != state.active || m.taskCompletionShown(worker.GetID()) {
				t.Fatal("obsolete completion changed conversation, pending input, or current-attempt visibility")
			}
			select {
			case chain := <-provider.requests:
				t.Fatalf("obsolete failure woke the model: %+v", chain)
			default:
			}
		})
	}
}

func TestTaskCompletionFenceKeepsGenuineNotices(t *testing.T) {
	for _, kind := range []string{"current attempt", "unscoped Bash", "unknown task", "ordinary message"} {
		t.Run(kind, func(t *testing.T) {
			m, provider := noticeDeliveryModel(t)
			m.conv.Stream.Active = true // This fixture already has a live agent session.
			m.services.Task = task.NewManager()
			worker := viewerWorker(m, "continued", "", time.Now())
			worker.info.Status, worker.info.Attempt = task.StatusFailed, 2
			worker.info.Error, worker.info.AgentSessionID = "current failure", "child"
			info := worker.info
			switch kind {
			case "unscoped Bash":
				info.ID, info.Type, info.Attempt = "bash", task.TaskTypeBash, 0
			case "unknown task":
				info.ID = "missing"
			case "ordinary message":
				info.Attempt = 1 // Quoted old completion text is not routing metadata.
			}
			message, _ := taskCompletionMessage(info)
			notice := fromBrokerMessage(message)
			if kind == "ordinary message" {
				notice.FinishedTaskID, notice.FinishedAttempt = "", 0
			}
			runCmd(m.deliverNotice(notice))
			if len(m.conv.Messages) != 1 || m.conv.Messages[0].Content != notice.Content {
				t.Fatalf("genuine notice was hidden: %+v", m.conv.Messages)
			}
			assertAgentRead(t, provider)
		})
	}
}

func TestBackgroundNoticeAfterManualStopWaitsForUser(t *testing.T) {
	m, provider := noticeDeliveryModel(t)
	m.backgroundNoticesHeld = true
	runCmd(m.deliverNotice(taskNotice()))
	assertNoticeShown(t, m)
	if m.conv.Stream.Active || len(m.pendingMessages) != 1 {
		t.Fatalf("background notice restarted the stopped conversation: streaming=%v pending=%d", m.conv.Stream.Active, len(m.pendingMessages))
	}
	if cmd := m.sendPendingMessages(); cmd != nil {
		t.Fatal("automatic queue drain bypassed manual stop")
	}
	runCmd(m.sendToAgent(pendingMessage{Message: core.UserMessage("Continue with the pending result", nil), fromUser: true}))
	if m.backgroundNoticesHeld || len(m.pendingMessages) != 0 {
		t.Fatal("real user input did not release retained notifications")
	}
	assertAgentRead(t, provider)
}

func TestViewedContinuationOnlyAsksMainAndRejectsStaleAttempt(t *testing.T) {
	for _, stale := range []bool{false, true} {
		m := interactionModel()
		m.services.sessionID = "parent"
		worker := viewerWorker(m, "stable", "parent", time.Now())
		worker.info.Status = task.StatusFailed
		worker.info.AgentSessionID, worker.info.Attempt, worker.info.Error = "child", 1, "provider unavailable"
		m.openTasks()
		action := input.TaskContinueMsg{TaskID: "stable", ViewID: m.userInput.Tasks.ViewID(), Attempt: 1}
		if stale {
			worker.info.Attempt = 2
		}
		m.continueViewedTask(action)
		if stale {
			if len(m.pendingMessages) != 0 || !m.userInput.Tasks.IsActive() {
				t.Fatal("stale continuation action affected a replacement execution")
			}
			continue
		}
		if len(m.pendingMessages) != 1 || m.userInput.Tasks.IsActive() || worker.info.Status != task.StatusFailed {
			t.Fatal("viewer launched a worker directly instead of asking main")
		}
		content := m.pendingMessages[0].Text()
		for _, want := range []string{"stable", "resume_task_id", "short corrective", "provider unavailable", "do not copy the full child transcript"} {
			if !strings.Contains(content, want) {
				t.Fatalf("continuation request lost %q: %s", want, content)
			}
		}
	}
}
