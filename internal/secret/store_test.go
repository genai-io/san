package secret

import (
	"path/filepath"
	"testing"
)

// Two stores on one file stand in for two San processes, each holding the
// map it loaded at startup.
func TestSetKeepsKeysWrittenByAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	a := &Store{path: path, data: map[string]string{}}
	b := &Store{path: path, data: map[string]string{}}

	if err := b.Set("B_KEY", "b"); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("A_KEY", "a"); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete("A_KEY"); err != nil {
		t.Fatal(err)
	}

	fresh := &Store{path: path, data: map[string]string{}}
	if err := fresh.load(); err != nil {
		t.Fatal(err)
	}
	if got := fresh.Get("B_KEY"); got != "b" {
		t.Fatalf("B_KEY = %q, want %q: another process's key was overwritten", got, "b")
	}
}
