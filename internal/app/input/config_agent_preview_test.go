package input

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/setting"
)

func TestAppearanceAgentPreviewIndependentStepsAndSaves(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	seven, five := 7, 5
	if err := setting.SaveBashPreviewLines(seven); err != nil {
		t.Fatal(err)
	}
	p := newAppearancePanel(setting.New(&setting.Data{BashPreviewLines: &seven, AgentPreviewLines: &five}))
	p.Enter()
	agent, bash := -1, -1
	for i, opt := range p.options {
		if opt.kind == kindAgentPreview {
			agent = i
		}
		if opt.kind == kindBashPreview {
			bash = i
		}
	}
	if agent < 0 || bash < 0 {
		t.Fatal("missing independent preview rows")
	}
	p.cursor = agent
	if p.Dirty() || p.options[agent].previewLines != five {
		t.Fatal("saved baseline not loaded")
	}
	press := func(key string, n int) {
		for range n {
			p.HandleKey(tea.KeyPressMsg{Code: []rune(key)[0], Text: key})
		}
	}
	press("l", 30)
	if p.options[agent].previewLines != setting.MaxAgentPreviewLines || !p.Dirty() {
		t.Fatal("upper bound or dirty state incorrect")
	}
	press("k", 1)
	p.cursor = agent
	if p.options[agent].previewLines != five || p.Dirty() {
		t.Fatal("navigation did not reset unsaved preview")
	}
	press("h", 30)
	if p.options[agent].previewLines != 0 || !strings.Contains(p.HintLine(), "fewer/more") {
		t.Fatal("lower bound or hint incorrect")
	}
	cmd, done := p.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !done || cmd == nil {
		t.Fatal("Enter did not save")
	}
	if msg, ok := cmd().(AgentPreviewSavedMsg); !ok || msg.Lines != 0 {
		t.Fatalf("saved message = %#v", msg)
	}
	if p.Dirty() || p.options[bash].previewLines != seven || p.previewBaseline != seven {
		t.Fatal("Agent save changed Bash preference or retained dirty state")
	}
	saved, err := setting.NewLoader().LoadFile(filepath.Join(home, ".san", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.AgentPreviewLines == nil || saved.AgentPreviewLineCount() != 0 || saved.BashPreviewLineCount() != seven {
		t.Fatalf("saved = %+v", saved)
	}
}
