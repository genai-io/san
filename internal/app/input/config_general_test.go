package input

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Auto update is opt-out, so turning it off must persist an explicit false —
// a dropped field would read back as on.
func TestGeneralPanelAutoUpdateOffPersistsExplicitFalse(t *testing.T) {
	home := tempHome(t)

	p := newGeneralPanel(nil)
	p.Enter()
	if !p.autoUpdate {
		t.Fatal("auto update should default on")
	}
	cmd, done := p.HandleKey(tea.KeyPressMsg{Code: tea.KeyRight}) // On → Off
	if done || p.saveErr != nil {
		t.Fatalf("switching should save in place: done=%v err=%v", done, p.saveErr)
	}
	if msg, ok := cmd().(AutoUpdateSavedMsg); !ok || msg.On {
		t.Fatalf("got %#v, want AutoUpdateSavedMsg{On:false}", cmd())
	}

	raw, err := os.ReadFile(filepath.Join(home, ".san", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		AutoUpdate *bool `json:"autoUpdate"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.AutoUpdate == nil || *data.AutoUpdate {
		t.Fatalf("persisted autoUpdate = %v, want explicit false", data.AutoUpdate)
	}
}
