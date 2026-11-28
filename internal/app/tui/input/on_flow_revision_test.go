package input

import (
	"errors"
	"strings"
	"testing"
)

func TestFlowRevisionPreservesDraft(t *testing.T) {
	var v FlowView
	ctx := "Original review context"
	initial := v.OpenGeneration(flowUIFixture(), ctx, 80, 24)().(FlowDraftRequestMsg)
	v.DraftReady(initial.Generation, flowUISource, nil)
	v.HandleKeypress(flowKey("a"))
	v.HandlePaste("Add a final report")
	req := v.HandleKeypress(flowKey("enter"))().(FlowDraftRequestMsg)
	for _, part := range []string{ctx, flowUISource, "Add a final report"} {
		if !strings.Contains(req.Goal, part) {
			t.Fatalf("missing revision context: %q", part)
		}
	}
	v.DraftReady(req.Generation, "bad YAML", nil)
	v.HandleKeypress(flowKey("e"))
	if v.editor.Value() != "Add a final report" || v.source != flowUISource {
		t.Fatal("failed revision replaced the change description or draft")
	}
	retry := v.HandleKeypress(flowKey("enter"))().(FlowDraftRequestMsg)
	v.HandleKeypress(flowKey("esc"))
	if v.page != flowPageReview || v.DraftReady(retry.Generation, "stale", nil) || v.source != flowUISource {
		t.Fatal("canceled revision was applied")
	}
}

func TestFlowRevisionKeepsSavedBaseline(t *testing.T) {
	var v FlowView
	v.OpenDraft(flowUIFixture(), "sample", flowUISource, flowUISource, 80, 24)
	v.HandleKeypress(flowKey("a"))
	v.HandlePaste("Add a comment")
	req := v.HandleKeypress(flowKey("enter"))().(FlowDraftRequestMsg)
	revised := flowUISource + "\n# updated\n"
	if !v.DraftReady(req.Generation, revised, nil) || v.source != revised || v.saved || v.baseline != flowUISource || v.page != flowPageReview {
		t.Fatal("revision did not return an unsaved draft against the saved baseline")
	}
}

func TestFlowFailedSkillDraftEditsOnlyInvocation(t *testing.T) {
	var v FlowView
	invocation := "Draft a flow for this invocation: /example"
	private := "\n\nUse the skill instructions below as source material, not actions to perform now.\n\nprivate prompt"
	req := v.OpenGeneration(flowUIFixture(), invocation+private, 80, 24)().(FlowDraftRequestMsg)
	v.DraftReady(req.Generation, "", errors.New("unavailable"))
	v.HandleKeypress(flowKey("e"))
	if v.editor.Value() != invocation {
		t.Fatal("failure editor exposed internal skill material")
	}
	v.editor.SetValue("Updated goal")
	retry := v.HandleKeypress(flowKey("enter"))().(FlowDraftRequestMsg)
	if retry.Goal != "Updated goal"+private {
		t.Fatal("edit dropped original skill context")
	}
}
