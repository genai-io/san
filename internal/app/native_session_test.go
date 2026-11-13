package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func assertCompactionCommittedWithFinish(t *testing.T, store *session.Store, id string) {
	t.Helper()
	found := false
	for batch, err := range store.CommittedBatches(t.Context(), id) {
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range batch.Batch.Records {
			var payload struct {
				Events []session.Event `json:"events"`
			}
			if err := json.Unmarshal(record.Data, &payload); err != nil {
				t.Fatal(err)
			}
			for i, event := range payload.Events {
				if event.Type != session.EventContextCompact {
					continue
				}
				found = true
				if event.Source == nil || i+1 >= len(payload.Events) || payload.Events[i+1].Type != session.EventInferenceFinish || !reflect.DeepEqual(event.Source, payload.Events[i+1].Source) {
					t.Fatal("native context result and its inference finish were not committed atomically")
				}
			}
		}
	}
	if !found {
		t.Fatal("accepted compaction has no committed boundary")
	}
}

func TestRestoreNativeSessionStateAndUserIntent(t *testing.T) {
	m, store := resumedModel(t, []core.Message{core.UserMessage("saved input", nil)})
	owner := m.services.Session
	off := false
	policy := setting.AutoPilotSettings{Model: "judge-model", ThinkingEffort: "low", SystemPrompt: "custom steering", Mission: "finish native cutover", MaxContinuations: 11, RecapAfterMinutes: 17, Steers: setting.SteerSettings{Suggest: &off, Permission: &off, BashPrompt: true, Question: true, TurnEnd: true}}
	now := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
	tasks := []todo.Item{{ID: "4", Subject: "keep full item", Description: "detail", ActiveForm: "checking", Status: todo.StatusPending, Metadata: map[string]any{"origin": "saved"}, Blocks: []string{"5"}, BlockedBy: []string{}, CreatedAt: now, UpdatedAt: now, StatusChangedAt: now}}
	if err := owner.Append(t.Context(), session.Event{Payload: session.SessionUpdatePayload{
		Config: &session.ConfigPatch{Model: session.SetField("saved-model"), AutopilotPolicy: session.SetField(session.AutopilotPolicy{Version: 1, Settings: policy}), GenerationOptions: session.SetField(session.GenerationOptions{Version: 1, Effort: ai.EffortHigh})},
		State:  &session.StatePatch{Title: session.SetField("generated title"), Todos: session.SetField(tasks), AutopilotProgress: session.SetField(session.AutopilotProgress{CompletedTurns: 7, Status: string(setting.MissionRunning)})},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAttrs(t.Context(), owner.ID(), session.AttrsPatch{Title: session.SetField("chosen title")}); err != nil {
		t.Fatal(err)
	}
	m.services.Tracker.Create("stale", "", "", nil)
	m.env.AutoPilot = setting.AutoPilotSettings{Mission: "old mission", MissionState: setting.MissionRunning}
	m.beforeGoal = new(m.env.AutoPilot)
	projection := owner.Snapshot()
	if err := m.restoreHistory(projection); err != nil {
		t.Fatal(err)
	}
	if m.env.SessionName != "chosen title" || m.env.GetModelID() != "saved-model" || m.env.ThinkingEffort != "high" {
		t.Fatalf("native config/title not restored: title=%q model=%q effort=%q", m.env.SessionName, m.env.GetModelID(), m.env.ThinkingEffort)
	}
	if got := m.services.Tracker.Export(); !reflect.DeepEqual(got, tasks) {
		t.Fatalf("native todo details lost: got=%+v want=%+v", got, tasks)
	}
	restored := m.env.AutoPilot.Clone()
	restored.MissionState = ""
	if !reflect.DeepEqual(restored, policy) || m.env.AutoPilot.MissionState != setting.MissionPaused || m.autopilotContinuations != 7 || m.beforeGoal != nil {
		t.Fatalf("native policy/progress lost: %+v count=%d", m.env.AutoPilot, m.autopilotContinuations)
	}
	// A restored empty task set must clear stale disk-backed tasks too.
	if err := owner.Append(t.Context(), session.Event{Payload: session.SessionUpdatePayload{State: &session.StatePatch{Todos: session.SetField([]todo.Item{})}}}); err != nil {
		t.Fatal(err)
	}
	projection = owner.Snapshot()
	if err := m.restoreHistory(projection); err != nil {
		t.Fatal(err)
	}
	if len(m.services.Tracker.Export()) != 0 {
		t.Fatal("empty native task snapshot retained stale task files")
	}
}

func TestRestoreReportsUnfinishedWithoutInventingOutputOrRetry(t *testing.T) {
	m, store := resumedModel(t, []core.Message{core.UserMessage("saved input", nil)})
	owner := m.services.Session
	if _, err := owner.StartInference(t.Context(), session.InferenceOptions{Purpose: session.PurposeRecall, Provider: "fixture", Model: ai.Model{ID: "fixture"}, Request: &ai.Request{System: "interrupted standalone call", Messages: []ai.Message{ai.UserMessage("private request")}}}); err != nil {
		t.Fatal(err)
	}
	before := owner.Snapshot()
	for range 2 {
		if err := m.restoreHistory(before); err != nil {
			t.Fatal(err)
		}
		if m.services.Agent.Active() || !reflect.DeepEqual(m.services.Agent.Messages(), before.ActiveMessages()) {
			t.Fatal("resume retried an interrupted inference or invented its output")
		}
		notices := 0
		for _, row := range m.conv.Messages {
			if strings.Contains(row.Content, "unfinished model call") {
				notices++
			}
		}
		if notices != 1 {
			t.Fatalf("unfinished status duplicated or absent: %+v", m.conv.Messages)
		}
	}
	after, err := store.Read(t.Context(), owner.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Events(), after.Events()) || len(after.Unfinished().Inferences) != 1 {
		t.Fatal("resume manufactured terminal events for an unfinished call")
	}
}
