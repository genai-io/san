//go:build unix

package atomicfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWriteFileHonorsUmaskAndExistingMode(t *testing.T) {
	if os.Getenv("SAN_TEST_UMASK") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWriteFileHonorsUmaskAndExistingMode$")
		cmd.Env = append(os.Environ(), "SAN_TEST_UMASK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("umask test: %v\n%s", err, out)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "file")
	previous := unix.Umask(0o077)
	defer unix.Umask(previous)
	if err := WriteFile(path, []byte("new"), 0o777); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("new mode = %v, err = %v", info, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("existing mode = %v, err = %v", info, err)
	}
}

func TestWriteFileRejectsNamedPipes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("data"), 0o600); err == nil {
		t.Fatal("named pipe replaced with a regular file")
	}
	if info, err := os.Stat(path); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("pipe changed: %v, %v", info, err)
	}
}
