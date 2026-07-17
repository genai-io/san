package desktop

import (
	"strings"
	"testing"
)

func TestRenderPlacesTranscriptAboveFooter(t *testing.T) {
	mgr := New()
	mgr.Resize(80, 20)
	mgr.SetFooter("────\n> type here")
	mgr.SetContent(Pane{ID: "c", Sig: "1", Render: func(w, h int) string {
		return strings.Repeat("transcript line\n", 50)
	}})

	out := mgr.Render()
	if !strings.Contains(out, "> type here") {
		t.Fatalf("render missing footer:\n%s", out)
	}
	if lines := strings.Count(out, "\n") + 1; lines != 20 {
		t.Fatalf("render height = %d lines, want the full 20", lines)
	}
}

func TestSetContentRebuildsOnlyOnChange(t *testing.T) {
	mgr := New()
	mgr.Resize(80, 20)
	calls := 0
	render := func(w, h int) string { calls++; return "x" }

	mgr.SetContent(Pane{ID: "c", Sig: "1", Render: render})
	mgr.SetContent(Pane{ID: "c", Sig: "1", Render: render}) // same sig + size → cached
	if calls != 1 {
		t.Fatalf("render called %d times, want 1 (cached on signature)", calls)
	}
	mgr.SetContent(Pane{ID: "c", Sig: "2", Render: render}) // new sig → rebuild
	if calls != 2 {
		t.Fatalf("render called %d times, want 2 after the signature changed", calls)
	}
}
