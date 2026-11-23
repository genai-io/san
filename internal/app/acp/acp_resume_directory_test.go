package acp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

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
	target, err := store.Create(t.Context(), session.CreateOptions{ID: "saved", InitialCWD: root})
	if err != nil {
		t.Fatal(err)
	}
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
	reopened, err := store.Open(t.Context(), "saved")
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
