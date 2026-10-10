package atomicfile

import (
	"bytes"
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
