package flow

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const storedDefinition = "---\nname: review\n---\n```mermaid\nflowchart LR\n  work\n```\n\n## work\nReview {{input.task}}\n"

func TestDefinitionStorageExactSourceAndValidation(t *testing.T) {
	dir := t.TempDir()
	if err := SaveDefinition("review", storedDefinition, "", dir); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDefinition("review", dir)
	if err != nil || got != storedDefinition {
		t.Fatalf("source = %q, error = %v", got, err)
	}
	w, err := FindDefinition("review", dir)
	if err != nil || w.Source != storedDefinition {
		t.Fatalf("parsed source = %+v, error = %v", w, err)
	}
	info, err := os.Stat(filepath.Join(dir, "review.md"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0644 {
		t.Fatalf("definition mode = %v", info.Mode())
	}
	if _, err := ReadDefinition("missing", dir); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing definition = %v", err)
	}
	for _, name := range []string{"../review", "/tmp/review", "a/b", ""} {
		if err := SaveDefinition(name, storedDefinition, "", dir); err == nil {
			t.Errorf("accepted unsafe name %q", name)
		}
	}
	if err := SaveDefinition("review", storedDefinition, ""); err == nil {
		t.Fatal("accepted unconfigured storage")
	}
	if err := SaveDefinition("different", storedDefinition, "", dir); err == nil {
		t.Fatal("accepted mismatched frontmatter name")
	}
	if err := SaveDefinition("review", "not a flow", storedDefinition, dir); err == nil {
		t.Fatal("accepted invalid definition")
	}
	if err := os.Remove(filepath.Join(dir, "review.md")); err != nil {
		t.Fatal(err)
	}
	if err := SaveDefinition("review", storedDefinition, storedDefinition, dir); err == nil || !strings.Contains(err.Error(), "was removed") {
		t.Fatalf("removed definition save = %v", err)
	}
}

func TestDefinitionConcurrentEditors(t *testing.T) {
	dir := t.TempDir()
	if err := SaveDefinition("review", storedDefinition, "", dir); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, suffix := range []string{"First edit", "Second edit"} {
		wg.Go(func() {
			results <- SaveDefinition("review", storedDefinition+suffix, storedDefinition, dir)
		})
	}
	wg.Wait()
	close(results)
	var saved, stale int
	for err := range results {
		switch {
		case err == nil:
			saved++
		case strings.Contains(err.Error(), "changed on disk"):
			stale++
		default:
			t.Fatalf("unexpected save error: %v", err)
		}
	}
	if saved != 1 || stale != 1 {
		t.Fatalf("concurrent edits: %d saved, %d stale", saved, stale)
	}
}

func TestStoreDurabilityAndIsolation(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	r := RunRecord{ID: "run-1", Definition: "exact\n", Inputs: map[string]string{"a": "original"}, Nodes: map[string]Checkpoint{"n": {Outcome: Outcome{Output: "kept"}}}}
	if err := s.Put(r); err != nil {
		t.Fatal(err)
	}
	r.Inputs["a"] = "changed"
	got, err := NewStore(dir).Get(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Definition != "exact\n" || got.Inputs["a"] != "original" {
		t.Fatalf("bad reload: %+v", got)
	}
	info, err := os.Stat(filepath.Join(dir, "run-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", info.Mode())
	}
}
func TestStoreTraversalAndWriteErrors(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, id := range []string{"../outside", "a/b", "", ".."} {
		if s.Put(RunRecord{ID: id}) == nil {
			t.Errorf("accepted %q", id)
		}
		if _, err := s.Get(id); err == nil {
			t.Errorf("read accepted %q", id)
		}
	}
	p := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if NewStore(p).Put(RunRecord{ID: "run"}) == nil {
		t.Fatal("expected write error")
	}
}
func TestStoreLockExclusion(t *testing.T) {
	dir := t.TempDir()
	a, b := NewStore(dir), NewStore(dir)
	release, err := a.Acquire("run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Acquire("run"); err == nil {
		t.Fatal("duplicate lock")
	}
	release()
	release, err = b.Acquire("run")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestStoreProcessLock(t *testing.T) {
	if dir := os.Getenv("SAN_WORKFLOW_LOCK_HELPER"); dir != "" {
		release, err := NewStore(dir).Acquire("process")
		if os.Getenv("SAN_WORKFLOW_LOCK_EXPECT_BUSY") == "1" {
			if !errors.Is(err, ErrRunActive) {
				t.Fatalf("expected active, got %v", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = release // Deliberately exit with an owned lock to simulate a crash.
		return
	}
	dir := t.TempDir()
	s := NewStore(dir)
	release, err := s.Acquire("process")
	if err != nil {
		t.Fatal(err)
	}
	run := func(busy bool) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestStoreProcessLock$")
		cmd.Env = append(os.Environ(), "SAN_WORKFLOW_LOCK_HELPER="+dir)
		if busy {
			cmd.Env = append(cmd.Env, "SAN_WORKFLOW_LOCK_EXPECT_BUSY=1")
		}
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v %s", err, b)
		}
	}
	run(true)
	release()
	run(false)
	// The child exited holding the lock; the operating system released it.
	release, err = s.Acquire("process")
	if err != nil {
		t.Fatalf("dead owner recovery: %v", err)
	}
	release()
}

func TestStoreListSkipsUnreadableRecords(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Put(RunRecord{ID: "good", Status: RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"broken.json": "{", "not.valid.json": "{}", "good.lock": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	records, err := s.List()
	if err != nil || len(records) != 1 || records[0].ID != "good" {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
}

func TestStorePruneKeepsNewestFinishedAndActiveRuns(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	base := time.Now()
	for i, status := range []RunStatus{RunSucceeded, RunFailed, RunRunning, RunStopped, RunSucceeded} {
		id := fmt.Sprintf("run-%d", i)
		if err := s.Put(RunRecord{ID: id, Status: status, StartedAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	// run-1 is finished but locked by a resuming executor.
	release, err := s.Acquire("run-1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := s.Prune(1); err != nil {
		t.Fatal(err)
	}
	records, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, ","); got != "run-4,run-2,run-1" {
		t.Fatalf("kept %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "run-0.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pruned run left its lock file: %v", err)
	}
}

func TestStoreMemoryDeepCopy(t *testing.T) {
	s := NewStore("")
	if err := s.Put(RunRecord{ID: "a", Inputs: map[string]string{"a": "b"}}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Get("a")
	r.Inputs["a"] = "c"
	r, _ = s.Get("a")
	if r.Inputs["a"] != "b" {
		t.Fatal("alias")
	}
}
