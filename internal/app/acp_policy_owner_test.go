package app

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPModeUsesSessionPolicy(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Says("done")), nil)
	id := f.session(t, t.TempDir())
	otherID := f.session(t, t.TempDir())
	s, other := f.host.sessions[id], f.host.sessions[otherID]
	s.policy.SetMode(setting.ModeAutoAccept)
	if got := s.modes().CurrentModeID; got != "auto-accept" {
		t.Fatalf("advertised mode = %q", got)
	}
	if got := other.modes().CurrentModeID; got != "normal" {
		t.Fatalf("mode leaked to another session: %q", got)
	}
	if err := s.recordState(); err != nil {
		t.Fatal(err)
	}
	history, err := s.data.Store.Read(string(id))
	if err != nil {
		t.Fatal(err)
	}
	if history.Metadata.Mode != "auto-accept" {
		t.Fatalf("persisted mode = %q", history.Metadata.Mode)
	}
}

func TestACPModePersistenceFailurePreservesPolicy(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Says("done")), nil)
	id := f.session(t, t.TempDir())
	s := f.host.sessions[id]
	if err := s.data.Session.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := f.host.setMode(context.Background(), protocol.SetSessionModeRequest{SessionID: id, ModeID: "auto-accept"})
	if err == nil {
		t.Fatal("mode changed without persistence")
	}
	if s.policy.CurrentMode() != setting.ModeNormal || s.modes().CurrentModeID != "normal" {
		t.Fatal("failed persistence changed policy or advertised mode")
	}
}
