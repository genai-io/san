package setting

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestShowGitBar(t *testing.T) {
	on, off := true, false
	for _, tt := range []struct {
		name string
		data *Data
		want bool
	}{
		{"nil", nil, false},
		{"unset", NewData(), false},
		{"on", &Data{GitBar: &on}, true},
		{"off", &Data{GitBar: &off}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.data.ShowGitBar(); got != tt.want {
				t.Fatalf("ShowGitBar = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGitBarMergeAndClone(t *testing.T) {
	on, off := true, false
	base := &Data{GitBar: &on}
	if !mergeSettings(base, NewData()).ShowGitBar() {
		t.Fatal("an unset overlay dropped the base preference")
	}
	if got := mergeSettings(base, &Data{GitBar: &off}); got.GitBar == nil || got.ShowGitBar() {
		t.Fatal("an explicit false must override true")
	}
	if !mergeSettings(&Data{GitBar: &off}, base).ShowGitBar() {
		t.Fatal("an explicit true must override false")
	}
	clone := base.Clone()
	if !clone.ShowGitBar() || clone.GitBar == base.GitBar {
		t.Fatal("clone must preserve and deep-copy the Git bar pointer")
	}
	*clone.GitBar = false
	if !base.ShowGitBar() {
		t.Fatal("changing a clone changed the original Git bar preference")
	}
	if NewData().Clone().GitBar != nil {
		t.Fatal("clone must preserve unset")
	}
}

func TestSaveGitBar(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := SaveTheme("dark"); err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{true, false} {
		if err := SaveGitBar(on); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(home, ".san", "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		var got Data
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.GitBar == nil || got.ShowGitBar() != on || got.Theme != "dark" {
			t.Fatalf("saved on=%v: GitBar=%v, theme=%q", on, got.GitBar, got.Theme)
		}
		// The independent controller reload must retain the choice.
		controller := New(NewData())
		if err := controller.Reload(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if controller.Snapshot().ShowGitBar() != on {
			t.Fatalf("reloaded on=%v as %v", on, controller.Snapshot().ShowGitBar())
		}
	}
}
