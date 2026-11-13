package session

import (
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/todo"
)

func TestNativePolicyAndTrackerStateRoundTrip(t *testing.T) {
	store := cleanStore(t)
	policy := AutopilotPolicy{Version: 1, Settings: setting.AutoPilotSettings{Model: "vendor/model", ThinkingEffort: "high", SystemPrompt: "custom steering", SystemPromptFile: "/tmp/steer.md", Mission: "finish migration", MaxContinuations: -1, RecapAfterMinutes: -1, Steers: setting.SteerSettings{Suggest: new(false), Permission: new(false), BashPrompt: true, Question: true, TurnEnd: true}}}
	owner := createTyped(t, store, CreateOptions{ID: "native-state", Config: &EventConfig{AutopilotPolicy: &policy}})
	now := time.Now().UTC().Truncate(time.Millisecond)
	items := []todo.Item{{ID: "first", Subject: "build", Description: "full description", ActiveForm: "building", Status: todo.StatusInProgress, Owner: "worker", Metadata: map[string]any{"scope": "session", "nested": map[string]any{"retained": true}}, Blocks: []string{"second"}, BlockedBy: []string{}, CreatedAt: now, UpdatedAt: now, StatusChangedAt: now}, {ID: "second", Subject: "deleted task", Status: todo.StatusDeleted, BlockedBy: []string{"first"}, Blocks: []string{}, CreatedAt: now, UpdatedAt: now, StatusChangedAt: now}}
	expectedPolicy, expectedItems := eventCopy(policy), eventCopy(items)
	progress := AutopilotProgress{CompletedTurns: 3, Status: string(setting.MissionPaused)}
	if err := owner.Append(t.Context(), Event{Payload: SessionUpdatePayload{State: &StatePatch{Todos: SetField(items), AutopilotProgress: SetField(progress)}}}); err != nil {
		t.Fatal(err)
	}
	// Caller mutation must not change persisted tracker metadata or tri-state policy.
	items[0].Metadata["scope"] = "changed"
	*policy.Settings.Steers.Permission = true
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), "native-state")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	projection := reopened.Snapshot()
	cfg, state := projection.Config(), projection.State()
	if !reflect.DeepEqual(cfg.AutopilotPolicy, &expectedPolicy) || !reflect.DeepEqual(state.Todos, &expectedItems) {
		t.Fatalf("native product values changed: config=%+v state=%+v", cfg, state)
	}
	if cfg.AutopilotPolicy.Settings.Mission != "finish migration" || cfg.AutopilotPolicy.Settings.SystemPrompt != "custom steering" || cfg.AutopilotPolicy.Settings.Model != "vendor/model" || *cfg.AutopilotPolicy.Settings.Steers.Permission || *cfg.AutopilotPolicy.Settings.Steers.Suggest {
		t.Fatalf("policy=%+v", cfg.AutopilotPolicy)
	}
	if !reflect.DeepEqual(state.AutopilotProgress, &progress) || state.Todos == nil || (*state.Todos)[0].Metadata["scope"] != "session" || (*state.Todos)[0].Description != "full description" || (*state.Todos)[0].Owner != "worker" || !reflect.DeepEqual((*state.Todos)[0].Blocks, []string{"second"}) || !reflect.DeepEqual((*state.Todos)[1].BlockedBy, []string{"first"}) || (*state.Todos)[1].Status != todo.StatusDeleted || !(*state.Todos)[0].StatusChangedAt.Equal(now) {
		t.Fatalf("state=%+v", state)
	}
	// Accessors detach maps and nested product values too.
	(*state.Todos)[0].Metadata["scope"] = "accessor mutation"
	*cfg.AutopilotPolicy.Settings.Steers.Permission = true
	again := reopened.Snapshot()
	if (*again.State().Todos)[0].Metadata["scope"] != "session" || *again.Config().AutopilotPolicy.Settings.Steers.Permission {
		t.Fatal("accessor leaked native state")
	}
	if err := reopened.Append(t.Context(), Event{Payload: SessionUpdatePayload{State: &StatePatch{Todos: SetField([]todo.Item{})}}}); err != nil {
		t.Fatal(err)
	}
	cleared := reopened.Snapshot()
	if cleared.State().Todos == nil || len(*cleared.State().Todos) != 0 {
		t.Fatal("explicit empty tracker not preserved")
	}
}

func TestNativePolicyRejectsProgressAndInvalidTracker(t *testing.T) {
	store := cleanStore(t)
	if _, err := store.Create(t.Context(), CreateOptions{ID: "invalid-policy", Config: &EventConfig{AutopilotPolicy: &AutopilotPolicy{Version: 1, Settings: setting.AutoPilotSettings{MissionState: setting.MissionRunning}}}}); err == nil {
		t.Fatal("progress accepted inside policy")
	}
	owner := createTyped(t, store, CreateOptions{ID: "tracker-validation"})
	for _, items := range [][]todo.Item{{{ID: "", Subject: "missing ID", Status: todo.StatusPending}}, {{ID: "one", Subject: "", Status: todo.StatusPending}}, {{ID: "one", Subject: "bad status", Status: "unknown"}}, {{ID: "one", Subject: "first", Status: todo.StatusPending}, {ID: "one", Subject: "duplicate", Status: todo.StatusDeleted}}} {
		if err := owner.Append(t.Context(), Event{Payload: SessionUpdatePayload{State: &StatePatch{Todos: SetField(items)}}}); err == nil {
			t.Fatalf("invalid tracker accepted: %+v", items)
		}
	}
	p := owner.Snapshot()
	if len(p.Events()) != 1 {
		t.Fatal("invalid state partially committed")
	}
}
