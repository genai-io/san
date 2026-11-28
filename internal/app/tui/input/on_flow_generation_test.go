package input

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestFlowGenerationShowsLiveProgress(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.OpenGeneration(s, "Review the parser", 96, 32)
	v.draftAt = time.Now().Add(-25 * time.Second)
	v.SetDraftFrame(3)
	view := ansi.Strip(v.Render())
	if view != "● Drafting flow   Esc cancel" {
		t.Fatalf("unexpected generation view: %q", view)
	}
	if v.phase != 3 || s.launches != 0 || s.saves != 0 {
		t.Fatal("animation must not start or save work")
	}
}

func TestFlowGenerationKeepsStatusVisible(t *testing.T) {
	for _, size := range [][2]int{{96, 32}, {40, 18}, {24, 10}, {40, 8}} {
		var v FlowView
		v.OpenGeneration(flowUIFixture(), strings.Repeat("检查安全和性能。", 100), size[0], size[1])
		view := ansi.Strip(v.Render())
		for _, want := range []string{"Drafting flow", "Esc cancel"} {
			if !strings.Contains(view, want) {
				t.Fatalf("size %v: missing %q in %q", size, want, view)
			}
		}
		if len(strings.Split(view, "\n")) > size[1] {
			t.Fatalf("generation view exceeds height %d", size[1])
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("size %v: line too wide: %q", size, line)
			}
		}
	}
}

func TestFlowGenerationRetryResetsProgress(t *testing.T) {
	var v FlowView
	request := v.OpenGeneration(flowUIFixture(), "Review", 80, 24)().(FlowDraftRequestMsg)
	v.draftAt = time.Now().Add(-time.Minute)
	v.SetDraftFrame(9)
	v.DraftReady(request.Generation, "", errors.New("request timed out"))
	v.SetDraftFrame(10)
	if v.phase != 9 || !v.draftAt.IsZero() || strings.Contains(v.Render(), "Generating blueprint") {
		t.Fatal("failed generation still animates")
	}
	retry := v.HandleKeypress(flowKey("enter"))().(FlowDraftRequestMsg)
	if retry.Generation == request.Generation || v.phase != 0 || time.Since(v.draftAt) > time.Second {
		t.Fatal("retry retained old generation or elapsed time")
	}
	v.HandleKeypress(flowKey("esc"))
	v.SetDraftFrame(11)
	if v.phase != 0 || !v.draftAt.IsZero() || v.Render() != "" {
		t.Fatal("closed generation still updates")
	}
}
