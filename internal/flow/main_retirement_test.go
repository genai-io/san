package flow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
)

type retiringMain struct {
	scriptedMain
	closed chan error
	runErr error
	onOpen func(MainRunInfo) error
	cancel bool
}

func (m *retiringMain) Open(_ context.Context, info MainRunInfo) (MainRunExecutor, error) {
	if m.onOpen != nil {
		if err := m.onOpen(info); err != nil {
			return m, err
		}
	}
	return m, nil
}
func (m *retiringMain) RunNode(ctx context.Context, c Call) (string, error) {
	if m.cancel {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if m.runErr != nil {
		return "", m.runErr
	}
	return m.scriptedMain.RunNode(ctx, c)
}
func (m *retiringMain) Close(err error) { m.closed <- err }

func TestMainLeaseRetiresWithOutcome(t *testing.T) {
	for _, kind := range []string{"success", "failed", "canceled", "setup-store-failure", "open-failure", "exhausted"} {
		t.Run(kind, func(t *testing.T) {
			tasks := task.NewManager()
			tasks.SetSessionID("native-session")
			s := NewService(tasks)
			s.store = NewStore(t.TempDir())
			lease := &retiringMain{closed: make(chan error, 1)}
			source := "```mermaid\nflowchart LR\n answer\n```\n## answer\nexecutor: main\n\nwrite\n"
			switch kind {
			case "failed":
				lease.runErr = errors.New("native turn failed")
			case "canceled":
				lease.cancel = true
			case "open-failure":
				lease.onOpen = func(MainRunInfo) error { return errors.New("setup failed after lease registration") }
			case "setup-store-failure":
				lease.onOpen = func(info MainRunInfo) error { return os.Mkdir(filepath.Join(s.store.dir, info.ID+".json"), 0700) }
			case "exhausted":
				source = mainLoopDefinition
				lease.pending = 3
				s.SetExecutor(&scriptedExecutor{outputs: map[string]string{"decision#1": "PASS", "decision#2": "PASS", "decision#3": "PASS"}})
			}
			s.SetMainExecutor(lease)
			b, err := Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			id, err := s.start(b, nil, "retirement", 100)
			if kind == "setup-store-failure" || kind == "open-failure" {
				if err == nil {
					t.Fatal("setup unexpectedly succeeded")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				bg, _ := tasks.Get(id)
				if kind == "canceled" {
					if err := bg.Stop(); err != nil {
						t.Fatal(err)
					}
				}
				if !bg.WaitForCompletion(5 * time.Second) {
					t.Fatal("flow incomplete")
				}
			}
			select {
			case closeErr := <-lease.closed:
				if (closeErr == nil) != (kind == "success") {
					t.Fatalf("close outcome=%v kind=%s", closeErr, kind)
				}
				if kind == "success" {
					records, err := s.Runs()
					if err != nil || len(records) != 1 || records[0].Status != RunSucceeded {
						t.Fatalf("closed before persistence: %v %v", records, err)
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("lease not retired")
			}
		})
	}
}
