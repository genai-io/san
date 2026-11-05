package task

import (
	"context"
	"os/exec"
	"testing"
)

func TestOwnerAgentContext(t *testing.T) {
	ctx := context.Background()
	if got := OwnerAgentIDFromContext(ctx); got != "" {
		t.Fatalf("default owner = %q", got)
	}
	ctx = WithOwnerAgentID(WithSessionID(ctx, "conversation"), "child")
	if got := OwnerAgentIDFromContext(ctx); got != "child" {
		t.Fatalf("child owner = %q", got)
	}
	ctx = WithOwnerAgentID(ctx, "")
	if got := OwnerAgentIDFromContext(ctx); got != "" {
		t.Fatalf("explicit main owner = %q", got)
	}
	if got := SessionIDFromContext(ctx); got != "conversation" {
		t.Fatalf("owner changed session = %q", got)
	}
}

func TestBashOwnerCapturedBeforeRegistration(t *testing.T) {
	observer := &testTaskObserver{}
	SetLifecycleHandler(observer)
	defer SetLifecycleHandler(nil)

	command := func() *exec.Cmd {
		cmd := exec.Command("echo")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Wait() })
		return cmd
	}
	m := NewManager()
	m.SetSessionID("other-conversation")
	ctx := WithOwnerAgentID(WithSessionID(context.Background(), "launch-conversation"), "launch-child")
	child := m.CreateBashTask(ctx, command(), "echo", "", func() {})
	m.SetSessionID("switched-conversation")
	main := m.CreateBashTask(context.Background(), command(), "echo", "", func() {})
	child.Complete(0, nil)
	if got := child.GetStatus(); got.OwnerAgentID != "launch-child" || got.ParentSessionID != "launch-conversation" {
		t.Fatalf("child ownership changed: %+v", got)
	}
	if got := main.GetStatus(); got.OwnerAgentID != "" || got.ParentSessionID != "switched-conversation" {
		t.Fatalf("main ownership = %+v", got)
	}
	if len(observer.created) != 2 || observer.created[0].OwnerAgentID != "launch-child" || observer.created[0].ParentSessionID != "launch-conversation" || observer.created[1].OwnerAgentID != "" {
		t.Fatalf("creation ownership = %+v", observer.created)
	}
	if len(observer.completed) != 1 || observer.completed[0].OwnerAgentID != "launch-child" {
		t.Fatalf("completion ownership = %+v", observer.completed)
	}
}

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
