package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/setting"
)

func newModelPickPanel() AutopilotSelector {
	p := NewAutopilotSelector()
	p.SetModelSource(func() []string {
		return []string{"anthropic/claude-haiku-4-5", "openai/gpt-5-mini", "openai/gpt-5-nano"}
	})
	p.SetEffortSource(func(ref string) []string {
		if ref == "openai/gpt-5-mini" {
			return []string{"low", "medium", "high"}
		}
		return nil
	})
	p.Enter(120, 40)
	return p
}

func press(p *AutopilotSelector, keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		p.HandleKeypress(k)
	}
}

func typeText(p *AutopilotSelector, s string) {
	for _, r := range s {
		p.HandleKeypress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

var (
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keySpace = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	keyLeft  = tea.KeyPressMsg{Code: tea.KeyLeft}
	keyRight = tea.KeyPressMsg{Code: tea.KeyRight}
)

// rowIndex finds a menu row by label so tests don't hard-code positions.
func rowIndex(t *testing.T, p *AutopilotSelector, label string) int {
	t.Helper()
	for i, r := range p.rows() {
		if r.label == label {
			return i
		}
	}
	t.Fatalf("no %q row", label)
	return -1
}

func TestAutopilotModelPickProviderModelThinking(t *testing.T) {
	p := newModelPickPanel()
	p.beginModelPick()

	// Rows: session model, anthropic, openai → openai, filter "mini", then a rung.
	press(&p, keyDown, keyDown, keyEnter)
	if p.pickVendor != "openai" {
		t.Fatalf("pickVendor = %q, want openai", p.pickVendor)
	}
	typeText(&p, "mini")
	press(&p, keyEnter)
	if p.pickModel != "openai/gpt-5-mini" {
		t.Fatalf("a reasoning model should open its thinking rungs, pickModel = %q", p.pickModel)
	}
	press(&p, keyDown, keyEnter) // default, low → low

	if p.snap.Model != "openai/gpt-5-mini" || p.snap.ThinkingEffort != "low" {
		t.Fatalf("model = %q effort = %q, want openai/gpt-5-mini low", p.snap.Model, p.snap.ThinkingEffort)
	}
	if p.view != apMenu {
		t.Fatalf("view = %v, want menu after pick", p.view)
	}

	// A model that doesn't reason skips the rung step and drops the old rung.
	p.beginModelPick()
	p.modelCursor = 1 // anthropic
	press(&p, keyEnter, keyEnter)
	if p.snap.Model != "anthropic/claude-haiku-4-5" || p.snap.ThinkingEffort != "" {
		t.Fatalf("model = %q effort = %q, want haiku with no rung", p.snap.Model, p.snap.ThinkingEffort)
	}

	// The provider list's first row resets to the session model.
	p.beginModelPick()
	p.modelCursor = 0
	press(&p, keyEnter)
	if p.snap.Model != "" {
		t.Fatalf("model = %q, want empty (session model)", p.snap.Model)
	}
}

func TestAutopilotEnterSavesEscDiscards(t *testing.T) {
	// No live source is wired here, so Enter keeps the buffer; Esc must not
	// emit anything for it either way.
	p := newModelPickPanel()
	p.snap.Model = "openai/gpt-5-mini"
	if cmd := p.HandleKeypress(keyEsc); p.IsActive() || cmd != nil {
		t.Fatal("esc should close without saving")
	}

	p.Enter(120, 40)
	if cmd := p.HandleKeypress(keyEnter); cmd != nil {
		t.Fatal("enter on an unedited panel emitted a save")
	}

	p.Enter(120, 40)
	p.snap.Model = "anthropic/claude-haiku-4-5"
	cmd := p.HandleKeypress(keyEnter)
	if p.IsActive() || cmd == nil {
		t.Fatal("enter on an edited panel should dismiss it and save")
	}
	if msg, ok := cmd().(AutopilotSavedMsg); !ok || msg.Config.Model != "anthropic/claude-haiku-4-5" {
		t.Fatalf("enter emitted %#v, want AutopilotSavedMsg with the picked model", cmd())
	}
}

func TestAutopilotAnswerDrivesBothSteers(t *testing.T) {
	p := newModelPickPanel()
	p.cursor = rowIndex(t, &p, "Answer")
	press(&p, keySpace)
	if !p.snap.Steers.Question || !p.snap.Steers.BashPrompt {
		t.Fatalf("Answer on should set both steers: %+v", p.snap.Steers)
	}
	press(&p, keySpace)
	if p.snap.Steers.Question || p.snap.Steers.BashPrompt {
		t.Fatalf("Answer off should clear both steers: %+v", p.snap.Steers)
	}
}

func TestAutopilotContinueNeedsMissionAndStepsItsCap(t *testing.T) {
	p := newModelPickPanel()
	p.cursor = rowIndex(t, &p, "Continue")
	press(&p, keySpace)
	if p.snap.Steers.TurnEnd {
		t.Fatal("Continue turned on without a mission")
	}

	p.snap.Mission = "ship the release"
	press(&p, keySpace)
	if !p.snap.Steers.TurnEnd {
		t.Fatal("Continue did not turn on with a mission")
	}

	// Default 20 → 50 → 100 → no limit, and back down.
	press(&p, keyRight, keyRight, keyRight)
	if !p.snap.ContinuationsUnlimited() {
		t.Fatalf("cap = %d, want no limit", p.snap.MaxContinuations)
	}
	press(&p, keyLeft)
	if p.snap.MaxContinuations != 100 {
		t.Fatalf("cap = %d, want 100", p.snap.MaxContinuations)
	}
	p.snap.MaxContinuations = 37 // hand-set, off the ladder
	press(&p, keyLeft)
	if p.snap.MaxContinuations != 20 {
		t.Fatalf("cap = %d, want 20 (nearest rung below 37)", p.snap.MaxContinuations)
	}
}

func TestAutopilotMissionRowShowsState(t *testing.T) {
	p := newModelPickPanel()
	if got := p.missionValue(); !strings.HasPrefix(got, "not set") {
		t.Fatalf("empty mission reads %q", got)
	}
	p.snap.Mission = "ship it"
	p.snap.MissionState = setting.MissionRunning
	p.baseline = p.snap.Clone()
	if got := p.missionValue(); got != "ship it · running" {
		t.Fatalf("mission reads %q, want running", got)
	}
	p.snap.Mission = "ship the docs" // an edit starts over
	if got := p.missionValue(); got != "ship the docs · ready" {
		t.Fatalf("edited mission reads %q, want ready", got)
	}
}
