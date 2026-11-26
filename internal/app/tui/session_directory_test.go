package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/session"
)

func TestValidateResumeDirectory(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, requested := range []string{dir, alias, filepath.Join(dir, ".")} {
		if err := session.ValidateResumeDirectory(dir, requested); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.ValidateResumeDirectory("", dir); err != nil {
		t.Fatalf("unknown saved cwd rejected: %v", err)
	}
	if err := session.ValidateResumeDirectory(dir, t.TempDir()); err == nil || !strings.Contains(err.Error(), "reopen San") {
		t.Fatalf("expected actionable mismatch, got %v", err)
	}
	if err := session.ValidateResumeDirectory(filepath.Join(dir, "missing"), dir); err == nil {
		t.Fatal("unavailable saved directory accepted")
	}
}

func TestResumeDirectoryMismatchPreservesSelectionAndReleasesLease(t *testing.T) {
	m, store := resumedModel(t, parallelResultChain())
	t.Cleanup(group.Forget)
	original := m.services.Session
	permissions := m.env.SessionPermissions
	target := mustCreateSession(t, store, session.CreateOptions{ID: "other", InitialCWD: t.TempDir()})
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.loadSessionByID("other"); err == nil || !strings.Contains(err.Error(), "directory mismatch") {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if m.services.Session != original || m.sessionID() != original.ID() || m.env.SessionPermissions != permissions {
		t.Fatal("failed resume replaced original session or permission context")
	}
	if _, err := store.Open(t.Context(), original.ID()); err == nil {
		t.Fatal("original lease was released")
	}
	reopened, err := store.Open(t.Context(), "other")
	if err != nil {
		t.Fatalf("rejected session lease retained: %v", err)
	}
	_ = reopened.Close()
}

func TestResumeDirectorySameDirectorySucceeds(t *testing.T) {
	m, store := resumedModel(t, parallelResultChain())
	t.Cleanup(group.Forget)
	target := mustCreateSession(t, store, session.CreateOptions{ID: "other", InitialCWD: m.env.CWD})
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.loadSessionByID("other"); err != nil {
		t.Fatal(err)
	}
	if m.sessionID() != "other" {
		t.Fatal("same-directory session not selected")
	}
}
