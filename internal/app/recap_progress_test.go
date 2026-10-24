package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/todo"
)

func TestRecapLiveUsesOnlyBreathingMark(t *testing.T) {
	for _, withFacts := range []bool{false, true} {
		m := newRecapModel(t)
		m.recap.busy, m.recap.show = true, true
		facts := ""
		if withFacts {
			item := m.services.Tracker.Create("first", "", "", nil)
			m.services.Tracker.Create("second", "", "", nil)
			if err := m.services.Tracker.Update(item.ID, todo.WithStatus(todo.StatusCompleted)); err != nil {
				t.Fatal(err)
			}
			facts = "  plan 1/2\n"
		}
		var faint, peak string
		for i := range 8 {
			raw := m.recapLive()
			got := ansi.Strip(raw)
			want := "※\n" + facts
			if got != want {
				t.Fatalf("facts=%v frame=%d: progress = %q, want only the exact ※ mark and optional facts %q", withFacts, i, got, want)
			}
			if i == 0 {
				faint = raw
			}
			if i == 3 {
				peak = raw
			}
			if strings.Contains(got, "writing") || strings.Contains(strings.ToLower(got), "recap") {
				t.Fatalf("progress must not contain a placeholder label: %q", got)
			}
			m.conv.Spinner, _ = m.conv.Spinner.Update(m.conv.Spinner.Tick())
		}
		if faint == peak {
			t.Fatal("the ※ mark must breathe across frames without changing its shape or text")
		}
	}
}

func TestRecapProgressStaysHiddenDuringBackgroundPreparation(t *testing.T) {
	m := newRecapModel(t)
	m.recap.busy = true
	if got := m.recapLive(); got != "" {
		t.Fatalf("background preparation must not expose a progress indicator: %q", got)
	}
}

func TestRecapCompletionKeepsStaticRecapMark(t *testing.T) {
	m := newRecapModel(t)
	m.recap.busy, m.recap.show = true, true
	m.onRecapReady(recapReadyMsg{sessionID: m.sessionID(), text: "Tests passed. Next, resolve the conflict."})
	if got := m.recapLive(); got != "" {
		t.Fatalf("completion must retire the breathing ※ mark: %q", got)
	}
	p := conv.RenderContext{Width: 80, Messages: m.conv.Messages}
	render := func() string { return conv.RenderSingleMessage(p, len(p.Messages)-1) }
	before := render()
	if !strings.HasPrefix(strings.TrimSpace(ansi.Strip(before)), "※ Tests passed.") || strings.Contains(ansi.Strip(before), "↩") {
		t.Fatalf("completion must keep the same ※ mark instead of a return arrow: %q", before)
	}
	for range 4 {
		m.conv.Spinner, _ = m.conv.Spinner.Update(m.conv.Spinner.Tick())
		if render() != before {
			t.Fatal("a completed recap must not keep animating")
		}
	}
}
