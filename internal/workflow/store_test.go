package workflow

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreDurabilityAndIsolation(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	r := RunRecord{ID: "run-1", Definition: "exact\n", Inputs: map[string]string{"a": "original"}, Nodes: map[string]Checkpoint{"n": {Output: "kept"}}}
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
	if runtime.GOOS == "windows" {
		return
	} // uncertain liveness is intentionally not stolen
	release, err = s.Acquire("process")
	if err != nil {
		t.Fatalf("dead owner recovery: %v", err)
	}
	release()
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
