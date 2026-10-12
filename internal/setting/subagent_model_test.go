package setting

import (
	"encoding/json"
	"testing"
)

func TestSubagentModelSurvivesConfigMergeCloneAndSnapshot(t *testing.T) {
	base := NewData()
	base.SubagentModel = "user-worker"
	overlay := NewData()
	if got := mergeSettings(base, overlay).SubagentModel; got != "user-worker" {
		t.Fatalf("unset overlay erased model: %q", got)
	}
	overlay.SubagentModel = "project-worker"
	merged := mergeSettings(base, overlay)
	if got := New(merged).SubagentModel(); got != "project-worker" {
		t.Fatalf("worker model = %q", got)
	}
	if got := New(merged).Snapshot().SubagentModel; got != "project-worker" {
		t.Fatalf("snapshot lost model: %q", got)
	}
	overlay.SubagentModel = "inherit"
	if got := mergeSettings(base, overlay).SubagentModel; got != "inherit" {
		t.Fatalf("explicit inheritance did not override model: %q", got)
	}
	var loaded Data
	if err := json.Unmarshal([]byte(`{"subagentModel":"vendor/worker"}`), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Clone().SubagentModel != "vendor/worker" {
		t.Fatal("JSON configuration lost worker model")
	}
	if got := New(nil).SubagentModel(); got != "" {
		t.Fatalf("unset model = %q", got)
	}
}
