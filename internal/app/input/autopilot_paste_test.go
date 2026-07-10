package input

import (
	"strings"
	"testing"
)

// The Mission editor is reachable only as an active overlay, and paste arrives
// as tea.PasteMsg (not a KeyMsg). The app drops paste for any overlay lacking
// HandlePaste, so these guard that AutopilotSelector routes it into the editors.

func TestAutopilotPasteIntoMission(t *testing.T) {
	p := NewAutopilotSelector()
	p.Enter(120, 40)
	p.openView(apMission)

	p.HandlePaste("ship the release\r\nthen tag it")

	if got, want := p.mission.input.Value(), "ship the release\nthen tag it"; got != want {
		t.Fatalf("mission = %q, want %q (CRLF should normalize to LF)", got, want)
	}
}

func TestAutopilotPasteIgnoredWhileRefining(t *testing.T) {
	p := NewAutopilotSelector()
	p.Enter(120, 40)
	p.openView(apMission)
	p.mission.refining = true

	p.HandlePaste("late arrival")

	if got := p.mission.input.Value(); got != "" {
		t.Fatalf("mission = %q, want empty (paste must not race an in-flight refine)", got)
	}
}

func TestAutopilotPasteIntoSystemPrompt(t *testing.T) {
	p := NewAutopilotSelector()
	p.Enter(120, 40)
	p.openView(apSystemPrompt)

	p.HandlePaste("\ndrive with care")

	if got := p.prompt.Value(); !strings.Contains(got, "drive with care") {
		t.Fatalf("system prompt = %q, want it to contain the pasted text", got)
	}
}

func TestAutopilotPasteInactiveIsNoop(t *testing.T) {
	p := NewAutopilotSelector()
	if cmd := p.HandlePaste("anything"); cmd != nil {
		t.Fatalf("HandlePaste on an inactive panel returned a cmd, want nil")
	}
}
