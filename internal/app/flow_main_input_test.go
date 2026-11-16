package app

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/flow"
)

func TestFlowMainWriterInputConsumesOnlyOnAdmission(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	lease, err := m.flowMain.bind(m.services.Session).Open(context.Background(), flow.MainRunInfo{ID: "run", SessionID: m.sessionID()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.flowMain.bind(nil) })
	m.userInput.Queue.Enqueue("用图书馆作例子", nil)
	m.flowMainRequest = &flowMainRequest{msg: flowMainMsg{call: &flow.Call{Node: &flow.Node{ID: "writer#2"}}}}
	message, _, admit, err := m.flowMainWriterInput("Write the next revision.")
	if err != nil || admit == nil || !strings.Contains(message.Text(), "用图书馆作例子") || !strings.Contains(message.Text(), "Write the next revision.") {
		t.Fatalf("queued amendment or full prompt missing from initial writer input: %v", err)
	}
	if display := m.conv.Messages[len(m.conv.Messages)-1].DisplayContent; display != "Flow step · writer#2\n\n用图书馆作例子" {
		t.Fatalf("writer input display is not concise: %q", display)
	}
	if m.userInput.Queue.Len() != 1 || lease.Instructions() != "" {
		t.Fatal("preparing an input consumed an unadmitted amendment")
	}
	admit()
	if m.userInput.Queue.Len() != 0 || lease.Instructions() != "用图书馆作例子" {
		t.Fatalf("accepted instructions not recorded exactly once: %q", lease.Instructions())
	}
	m.userInput.Queue.Enqueue("留到下一轮", nil)
	admit()
	if m.userInput.Queue.Len() != 1 || lease.Instructions() != "用图书馆作例子" {
		t.Fatal("repeated admission consumed a later revision's input")
	}
}

func TestFlowMainWriterInputPreservesEditing(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.userInput.Queue.Enqueue("still editing", nil)
	m.userInput.Queue.SelectIdx = 0
	if _, _, admit, err := m.flowMainWriterInput("Write"); err == nil || admit != nil || m.userInput.Queue.Len() != 1 {
		t.Fatal("editing queue item was admitted")
	}
}
