package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestValidateResumeDirectory(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, requested := range []string{dir, alias, filepath.Join(dir, ".")} {
		if err := validateResumeDirectory(dir, requested); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateResumeDirectory("", dir); err != nil {
		t.Fatalf("unknown legacy cwd rejected: %v", err)
	}
	if err := validateResumeDirectory(dir, t.TempDir()); err == nil || !strings.Contains(err.Error(), "reopen San") {
		t.Fatalf("expected actionable mismatch, got %v", err)
	}
	if err := validateResumeDirectory(filepath.Join(dir, "missing"), dir); err == nil {
		t.Fatal("unavailable saved directory accepted")
	}
}

func TestResumeDirectoryMismatchPreservesSelectionAndReleasesLease(t *testing.T) {
	m, store := resumedModel(t, parallelResultChain())
	t.Cleanup(group.Forget)
	original := m.services.Session
	permissions := m.env.SessionPermissions
	target := mustCreateSession(t, store, session.CreateOptions{ID: "other", Cwd: t.TempDir()})
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.loadSessionByID("other"); err == nil || !strings.Contains(err.Error(), "directory mismatch") {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if m.services.Session != original || m.sessionID() != original.ID() || m.env.SessionPermissions != permissions {
		t.Fatal("failed resume replaced original session or permission context")
	}
	if _, _, err := store.Open(original.ID()); err == nil {
		t.Fatal("original lease was released")
	}
	reopened, _, err := store.Open("other")
	if err != nil {
		t.Fatalf("rejected session lease retained: %v", err)
	}
	_ = reopened.Close()
}

func TestResumeDirectorySameDirectorySucceeds(t *testing.T) {
	m, store := resumedModel(t, parallelResultChain())
	t.Cleanup(group.Forget)
	target := mustCreateSession(t, store, session.CreateOptions{ID: "other", Cwd: m.env.CWD})
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

func TestACPResumeDirectoryGuard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	other := filepath.Join(root, "subdir")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := session.OpenStore(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	target := mustCreateSession(t, store, session.CreateOptions{ID: "saved", Cwd: root})
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	host := &acpHost{provider: acpTestProvider{aitest.Always(aitest.Says("done")).Client()}, model: "test", sessions: make(map[protocol.SessionID]*acpSession)}
	if _, err := host.open(t.Context(), protocol.NewSessionRequest{CWD: other}, "saved"); err == nil || !strings.Contains(err.Error(), "directory mismatch") {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if len(host.sessions) != 0 {
		t.Fatal("rejected resume registered runtime")
	}
	reopened, _, err := store.Open("saved")
	if err != nil {
		t.Fatalf("rejected ACP resume retained lease: %v", err)
	}
	_ = reopened.Close()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	response, err := host.open(t.Context(), protocol.NewSessionRequest{CWD: alias}, "saved")
	if err != nil {
		t.Fatalf("equivalent directory rejected: %v", err)
	}
	if err := host.sessions[response.SessionID].close(); err != nil {
		t.Fatal(err)
	}
}
