package input

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAutopilotMissionExitFencesLateRefinement(t *testing.T) {
	for _, delivery := range []string{"menu", "reopened", "successor"} {
		t.Run(delivery, func(t *testing.T) {
			p := NewAutopilotSelector()
			p.active, p.view = true, apMission
			p.mission.input.SetValue("original draft")
			p.SetMissionRefiner(func(string) (func(context.Context) (string, error), func() (bool, error)) {
				return func(context.Context) (string, error) { return "late rewrite", nil }, nil
			})
			batch := p.refineMission()().(tea.BatchMsg)
			old := batch[len(batch)-1]().(MissionRefinedMsg)
			p.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
			if p.view != apMenu || p.mission.refining || p.snap.Mission != "original draft" {
				t.Fatal("leaving mission editor did not preserve draft and end refinement")
			}
			if delivery != "menu" {
				p.openMission()
				if p.Thinking() {
					t.Fatal("reopened editor retained abandoned spinner")
				}
			}
			var successor MissionRefinedMsg
			if delivery == "successor" {
				batch = p.refineMission()().(tea.BatchMsg)
				successor = batch[len(batch)-1]().(MissionRefinedMsg)
			}
			p.DeliverRefinement(old)
			if p.mission.input.Value() != "original draft" || p.snap.Mission != "original draft" || p.mission.refining != (delivery == "successor") {
				t.Fatal("late reply changed draft or successor spinner")
			}
			if delivery == "menu" {
				p.openMission()
				if p.Thinking() {
					t.Fatal("reply discarded on menu blocked reopened editor")
				}
			}
			if delivery == "successor" {
				p.DeliverRefinement(successor)
				if p.Thinking() || p.mission.input.Value() != "late rewrite" {
					t.Fatal("successor refinement could not complete")
				}
			}
		})
	}
}

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
