package app

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPModeUsesSessionPolicy(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Says("done")), nil)
	id := f.session(t, t.TempDir())
	otherID := f.session(t, t.TempDir())
	s, other := f.host.sessions[id], f.host.sessions[otherID]
	s.permissions.SetMode(setting.ModeAutoAccept)
	if got := s.modes().CurrentModeID; got != "auto-accept" {
		t.Fatalf("advertised mode = %q", got)
	}
	if got := other.modes().CurrentModeID; got != "normal" {
		t.Fatalf("mode leaked to another session: %q", got)
	}
	if err := s.recordState(); err != nil {
		t.Fatal(err)
	}
	history, err := s.nativeSession.Store.Read(string(id), session.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if history.Metadata.Mode != "auto-accept" {
		t.Fatalf("persisted mode = %q", history.Metadata.Mode)
	}
}

func TestACPDirectoryPoliciesShareLivePermissions(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Says("done")), nil)
	cwd := t.TempDir()
	id := f.session(t, cwd)
	permissions := f.host.sessions[id].permissions
	parent := permissions.ForDirectory(cwd)
	worker := permissions.ForDirectory(t.TempDir())
	if parent.Session != permissions.SessionPermissions || worker.Session != parent.Session {
		t.Fatal("directory policies do not share the session grant owner")
	}

	permissions.SetMode(setting.ModeAutoAccept)
	args := map[string]any{"file_path": "shared.txt", "content": "test"}
	if got := parent.Decide("Write", args).Decision; got != perm.Permit {
		t.Fatalf("parent did not observe mode change: %v", got)
	}
	if got := worker.Decide("Write", args).Decision; got != perm.Prompt {
		t.Fatalf("worker lost execution-directory boundary: %v", got)
	}

	permissions.AllowPattern("Bash(make test)")
	for _, policy := range []agent.PermissionPolicy{parent, worker} {
		if got := policy.Decide("Bash", map[string]any{"command": "make test"}).Decision; got != perm.Permit {
			t.Fatalf("existing directory policy did not observe grant: %v", got)
		}
	}
	permissions.SetMode(setting.ModeNormal)
	if got := parent.Decide("Write", args).Decision; got != perm.Prompt {
		t.Fatalf("existing directory policy retained stale mode: %v", got)
	}
}

func TestACPModePersistenceFailurePreservesPolicy(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Says("done")), nil)
	id := f.session(t, t.TempDir())
	s := f.host.sessions[id]
	if err := s.nativeSession.Session.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := f.host.setMode(context.Background(), protocol.SetSessionModeRequest{SessionID: id, ModeID: "auto-accept"})
	if err == nil {
		t.Fatal("mode changed without persistence")
	}
	if s.permissions.CurrentMode() != setting.ModeNormal || s.modes().CurrentModeID != "normal" {
		t.Fatal("failed persistence changed policy or advertised mode")
	}
}
