package task

import (
	"context"
	"os/exec"
	"testing"
)

func TestWorkersKeepTheirLaunchingSession(t *testing.T) {
	m := NewManager()
	m.SetSessionID("first")
	ctx, cancel := context.WithCancel(WithSessionID(context.Background(), "first"))
	defer cancel()
	first := m.CreateAgentTask("first-worker", "", "", ctx, cancel)
	m.SetSessionID("second")
	nested := m.CreateAgentTask("nested-worker", "", "", ctx, cancel)
	current := m.CreateAgentTask("second-worker", "", "", context.Background(), cancel)
	for _, task := range []*AgentTask{first, nested} {
		if task.GetStatus().ParentSessionID != "first" {
			t.Fatal("worker followed the manager into another session")
		}
	}
	if current.GetStatus().ParentSessionID != "second" {
		t.Fatal("unscoped launch did not adopt the selected session")
	}
	cmd := exec.Command("echo", "nested")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	shell := m.CreateBashTask(ctx, cmd, "echo nested", "", cancel)
	if shell.GetStatus().ParentSessionID != "first" {
		t.Fatal("background shell lost its worker's session")
	}
}
