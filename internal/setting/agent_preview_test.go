package setting

import (
	"path/filepath"
	"testing"
)

func TestAgentPreviewLineCount(t *testing.T) {
	if got := (*Data)(nil).AgentPreviewLineCount(); got != 3 {
		t.Fatalf("nil default = %d", got)
	}
	if got := NewData().AgentPreviewLineCount(); got != 3 {
		t.Fatalf("unset default = %d", got)
	}
	for _, tc := range []struct{ value, want int }{{0, 0}, {5, 5}, {-1, 0}, {99, 20}} {
		if got := (&Data{AgentPreviewLines: &tc.value}).AgentPreviewLineCount(); got != tc.want {
			t.Errorf("value %d: got %d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestAgentPreviewMergeAndClone(t *testing.T) {
	five, zero, seven := 5, 0, 7
	base := &Data{AgentPreviewLines: &five, BashPreviewLines: &seven}
	if got := mergeSettings(base, NewData()); got.AgentPreviewLineCount() != 5 {
		t.Fatal("unset overlay dropped base")
	}
	got := mergeSettings(base, &Data{AgentPreviewLines: &zero})
	if got.AgentPreviewLines == nil || got.AgentPreviewLineCount() != 0 || got.BashPreviewLineCount() != 7 {
		t.Fatal("explicit zero must override only Agent preview")
	}
	clone := base.Clone()
	*clone.AgentPreviewLines = 9
	if base.AgentPreviewLineCount() != 5 || clone.AgentPreviewLines == base.AgentPreviewLines {
		t.Fatal("clone shares preview pointer")
	}
	if NewData().Clone().AgentPreviewLines != nil {
		t.Fatal("clone must preserve unset")
	}
}

func TestSaveAgentPreviewLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := SaveBashPreviewLines(7); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{5, 0} {
		if err := SaveAgentPreviewLines(n); err != nil {
			t.Fatal(err)
		}
		saved, err := NewLoader().LoadFile(filepath.Join(home, ".san", "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if saved.AgentPreviewLines == nil || *saved.AgentPreviewLines != n || saved.BashPreviewLineCount() != 7 {
			t.Fatalf("saved = %+v", saved)
		}
		controller := New(NewData())
		if err := controller.Reload(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if got := controller.Snapshot().AgentPreviewLineCount(); got != n {
			t.Fatalf("reload = %d, want %d", got, n)
		}
	}
}
