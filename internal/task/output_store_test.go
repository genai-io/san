package task

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentTaskInitializesStableOutputFile(t *testing.T) {
	tmpDir := t.TempDir()
	manager := NewManager()
	if err := manager.SetOutputDir(tmpDir); err != nil {
		t.Fatalf("SetOutputDir() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	task := manager.CreateAgentTask("task-output-1", "Explore", "Inspect code", ctx, cancel)
	if task.OutputFile == "" {
		t.Fatal("expected output file path to be assigned")
	}
	if _, err := os.Stat(task.OutputFile); err != nil {
		t.Fatalf("expected output file to exist: %v", err)
	}
}

func TestAgentTaskAppendOutputWritesToOutputFile(t *testing.T) {
	tmpDir := t.TempDir()
	manager := NewManager()
	if err := manager.SetOutputDir(tmpDir); err != nil {
		t.Fatalf("SetOutputDir() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	task := manager.CreateAgentTask("task-output-2", "Explore", "Inspect code", ctx, cancel)
	task.AppendOutput([]byte("first line\n"))
	task.AppendProgress("Read(main.go)")

	data, err := os.ReadFile(task.OutputFile)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"event":"task.started"`, `"event":"task.output"`, `"event":"task.progress"`, "first line", "Read(main.go)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output file missing %q: %q", want, text)
		}
	}
}

func TestManagersKeepOutputDirectoriesIndependent(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	first := NewManager()
	second := NewManager()
	if err := first.SetOutputDir(firstDir); err != nil {
		t.Fatalf("first SetOutputDir: %v", err)
	}
	if err := second.SetOutputDir(secondDir); err != nil {
		t.Fatalf("second SetOutputDir: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstTask := first.CreateAgentTask("first", "Explore", "first", ctx, cancel)
	secondTask := second.CreateAgentTask("second", "Explore", "second", ctx, cancel)

	if filepath.Dir(firstTask.OutputFile) != firstDir {
		t.Fatalf("first output = %q, want directory %q", firstTask.OutputFile, firstDir)
	}
	if filepath.Dir(secondTask.OutputFile) != secondDir {
		t.Fatalf("second output = %q, want directory %q", secondTask.OutputFile, secondDir)
	}
}
