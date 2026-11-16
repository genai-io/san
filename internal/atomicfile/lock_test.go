package atomicfile

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLockSerializesProcesses(t *testing.T) {
	if path := os.Getenv("SAN_TEST_LOCK_COUNTER"); path != "" {
		for range 20 {
			if err := WithLock(path+".lock", func() error {
				var count int
				if err := ReadJSON(path, &count); err != nil {
					return err
				}
				return WriteJSON(path, count+1, 0o600)
			}); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	path := filepath.Join(t.TempDir(), "counter.json")
	var commands []*exec.Cmd
	var outputs []*bytes.Buffer
	for range 2 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLockSerializesProcesses$")
		cmd.Env = append(os.Environ(), "SAN_TEST_LOCK_COUNTER="+path)
		output := new(bytes.Buffer)
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
		outputs = append(outputs, output)
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("counter writer: %v\n%s", err, outputs[i])
		}
	}
	var count int
	if err := ReadJSON(path, &count); err != nil {
		t.Fatal(err)
	}
	if count != 40 {
		t.Fatalf("counter = %d, want 40", count)
	}
}

func TestTryLockExcludesHandlesAndProcesses(t *testing.T) {
	if path := os.Getenv("SAN_TEST_TRYLOCK"); path != "" {
		release, err := TryLock(path)
		if os.Getenv("SAN_TEST_TRYLOCK_BUSY") == "1" {
			if !errors.Is(err, ErrLocked) {
				t.Fatalf("expected ErrLocked, got %v", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = release // Exit while holding the lock: the OS must drop it.
		return
	}
	path := filepath.Join(t.TempDir(), "run.lock")
	release, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second handle in one process = %v", err)
	}
	child := func(busy bool) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestTryLockExcludesHandlesAndProcesses$")
		cmd.Env = append(os.Environ(), "SAN_TEST_TRYLOCK="+path)
		if busy {
			cmd.Env = append(cmd.Env, "SAN_TEST_TRYLOCK_BUSY=1")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
	}
	child(true)
	release()
	release()
	child(false)
	release, err = TryLock(path)
	if err != nil {
		t.Fatalf("lock of an exited holder was not released: %v", err)
	}
	release()
}
