package input

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAutopilotMissionExpiredResultClearsSpinnerWithoutDraftMutation(t *testing.T) {
	p := NewAutopilotSelector()
	p.active, p.view = true, apMission
	p.mission.input.SetValue("original draft")
	ctx, cancel := context.WithCancel(t.Context())
	p.SetMissionRefiner(func(string) (func(context.Context) (string, error), func() (bool, error)) {
		return func(context.Context) (string, error) { return "expired rewrite", nil }, func() (bool, error) { return true, ctx.Err() }
	})
	batch := p.refineMission()().(tea.BatchMsg)
	msg := batch[len(batch)-1]().(MissionRefinedMsg)
	cancel() // The provider succeeded, but the request expired before UI delivery.
	p.DeliverRefinement(msg)
	if p.mission.refining || p.mission.input.Value() != "original draft" || !strings.Contains(p.mission.status, context.Canceled.Error()) {
		t.Fatalf("expired result: %+v", p.mission)
	}
	// A result from an earlier editor generation cannot clear its successor.
	batch = p.refineMission()().(tea.BatchMsg)
	old := batch[len(batch)-1]().(MissionRefinedMsg)
	p.refineMission()
	p.DeliverRefinement(old)
	if !p.mission.refining || p.mission.input.Value() != "original draft" {
		t.Fatal("old editor result cleared successor")
	}
}
