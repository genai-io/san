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
