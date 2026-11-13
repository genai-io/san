package app

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func autopilotRecordingModel(t *testing.T) (*model, *session.Session) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "autopilot"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	m := newSuggestionModel(t, true, setting.ModeAutoPilot)
	m.services.Recording, m.services.sessionID = owner, owner.ID()
	t.Cleanup(m.cancelAutopilot)
	return m, owner
}

func TestAutopilotRecordingParseAttempts(t *testing.T) {
	m, owner := autopilotRecordingModel(t)
	old := autopilotSteerBackoff
	autopilotSteerBackoff = 0
	defer func() { autopilotSteerBackoff = old }()
	calls := 0
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		calls++
		text := "not JSON"
		if calls == 2 {
			text = `{"continue":true,"instruction":"run final tests"}`
		}
		return aitest.Replies(ai.Response{Content: ai.TextContent(text), Usage: ai.Usage{Input: 17, Output: 4}})(ctx, req)
	})
	provider := &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "resolved"})}
	r := m.newAutopilotRequest()
	cont, _, instruction, err := autopilotDecideContinue(r.ctx, provider, "alias", "system", "mission", "evidence", "")
	if err != nil || !cont || instruction != "run final tests" {
		t.Fatalf("decision: %v %q %v", cont, instruction, err)
	}
	p := owner.Snapshot()
	events := p.Events()
	if len(events) != 4 || events[1].Type != session.EventInferenceStart || events[2].Type != session.EventInferenceFinish || events[3].Type != session.EventInferenceStart {
		t.Fatalf("attempt ordering: %+v", events)
	}
	start := events[1].Payload.(*session.InferenceStartPayload)
	if start.Purpose != session.PurposeAutopilot {
		t.Fatalf("purpose: %q", start.Purpose)
	}
	finish := events[2].Payload.(*session.InferenceFinishPayload)
	if finish.Usage.Input != 17 || finish.Outcome != session.OutcomeSuccess {
		t.Fatalf("parse accounting: %+v", finish)
	}
	final := r.take()
	msg := core.UserMessage(instruction, nil)
	msg.ID = core.NewMessageID()
	if err := final.FinishMessage(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	snapshot := owner.Snapshot()
	if len(snapshot.Messages()) != 1 || snapshot.Messages()[0].Content.Text() != instruction || len(snapshot.Unfinished().Inferences) != 0 {
		t.Fatal("final accepted instruction was not recorded")
	}
	if driver.Last().System != "system" || driver.Calls() != 2 {
		t.Fatalf("native request changed: %+v", driver.Last())
	}
}

func TestAutopilotRecordingClosedAdmissionStopsRetry(t *testing.T) {
	m, owner := autopilotRecordingModel(t)
	r := m.newAutopilotRequest()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	provider := &autopilotStubProvider{content: `{"done":true}`}
	_, _, _, err := autopilotDecideContinue(r.ctx, provider, "m", "system", "mission", "evidence", "")
	var admission *ai.AdmissionError
	if !errors.As(err, &admission) || provider.calls() != 0 {
		t.Fatalf("closed recording dispatched: %d %v", provider.calls(), err)
	}
}

func TestAutopilotRecordingParseCloseFailureStopsRetry(t *testing.T) {
	m, owner := autopilotRecordingModel(t)
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		return aitest.Says("invalid JSON")(ctx, req)
	})
	provider := &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "resolved"})}
	r := m.newAutopilotRequest()
	_, _, _, err := autopilotDecideContinue(r.ctx, provider, "m", "system", "mission", "evidence", "")
	var recordingErr *llm.RecordingError
	if !errors.As(err, &recordingErr) || driver.Calls() != 1 {
		t.Fatalf("recording failure retried: %d %v", driver.Calls(), err)
	}
}

func TestAutopilotRecordingMissionDraftOnly(t *testing.T) {
	m, owner := autopilotRecordingModel(t)
	driver := aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent("Refined mission."), Usage: ai.Usage{Input: 11, Output: 3}}))
	m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "resolved"}, ai.WithTemperature(0.4))}
	run, current := m.missionRefine("draft")
	// Preparation has already captured the original owner on the UI goroutine.
	text, err := run(t.Context())
	valid, currentErr := current()
	if err != nil || currentErr != nil || text != "Refined mission." || !valid {
		t.Fatalf("refine: %q %v", text, err)
	}
	p := owner.Snapshot()
	events := p.Events()
	if len(events) != 3 || len(p.Messages()) != 0 || len(p.Unfinished().Inferences) != 0 {
		t.Fatalf("draft invented a domain result: %+v", events)
	}
	if events[1].Payload.(*session.InferenceStartPayload).Purpose != session.PurposeAutopilot || events[2].Payload.(*session.InferenceFinishPayload).Usage.Input != 11 {
		t.Fatal("missing mission accounting")
	}
	if req := driver.Last(); req.MaxTokens != 600 || req.Temperature == nil || *req.Temperature != 0.4 || req.System != missionRefinePrompt {
		t.Fatalf("lost native defaults: %+v", req)
	}
	rebuilt, err := owner.InferenceRequest(t.Context(), events[1].Seq)
	if err != nil || !reflect.DeepEqual(rebuilt, driver.Last()) {
		t.Fatalf("native request rebuild: %+v %v", rebuilt, err)
	}
}

func TestAutopilotRecordingPartialProviderFailure(t *testing.T) {
	m, owner := autopilotRecordingModel(t)
	boom := errors.New("stream failed")
	driver := aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent("partial"), Usage: ai.Usage{Input: 19, Output: 2}, Err: boom}))
	provider := &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "resolved"})}
	r := m.newAutopilotRequest()
	_, _, _, err := autopilotDecideContinue(r.ctx, provider, "alias", "system", "mission", "evidence", "")
	if !errors.Is(err, boom) {
		t.Fatalf("lost provider error: %v", err)
	}
	p := owner.Snapshot()
	events := p.Events()
	if len(events) != 3 || events[2].Type != session.EventInferenceFinish {
		t.Fatalf("unclosed failed attempt: %+v", events)
	}
	finish := events[2].Payload.(*session.InferenceFinishPayload)
	if finish.Usage.Input != 19 || finish.Usage.Output != 2 || finish.Outcome != session.OutcomeFailure {
		t.Fatalf("partial usage: %+v", finish)
	}
}

func TestAutopilotRecordingCancelledWhileInFlight(t *testing.T) {
	m, owner := autopilotRecordingModel(t)
	entered := make(chan struct{})
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		close(entered)
		return aitest.Hangs()(ctx, req)
	})
	provider := &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "resolved"})}
	r := m.newAutopilotRequest()
	done := make(chan error, 1)
	go func() {
		_, _, _, err := autopilotDecideContinue(r.ctx, provider, "alias", "system", "mission", "evidence", "")
		done <- err
	}()
	<-entered
	m.cancelAutopilot()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error: %v", err)
	}
	p := owner.Snapshot()
	events := p.Events()
	if len(events) != 3 || len(p.Unfinished().Inferences) != 0 || events[2].Payload.(*session.InferenceFinishPayload).Outcome != session.OutcomeCancelled {
		t.Fatalf("cancel not accounted: %+v", events)
	}
	if cmd := m.handleAutopilotDecision(autopilotDecisionMsg{request: r, cont: true, instruction: "old"}); cmd != nil || len(p.Messages()) != 0 {
		t.Fatal("cancelled result submitted")
	}
}

func TestAutopilotRecordingDoneProgress(t *testing.T) {
	m, owner := autopilotRecordingModel(t)
	m.env.AutoPilot.Mission, m.env.AutoPilot.MissionState = "mission", setting.MissionRunning
	provider := &autopilotStubProvider{content: `{"done":true}`}
	r := m.newAutopilotRequest()
	cont, done, instruction, err := autopilotDecideContinue(r.ctx, provider, "m", "system", "mission", "evidence", "")
	if err != nil {
		t.Fatal(err)
	}
	m.handleAutopilotDecision(autopilotDecisionMsg{request: r, cont: cont, done: done, instruction: instruction})
	p := owner.Snapshot()
	events := p.Events()
	if len(events) != 4 || events[2].Type != session.EventSessionUpdate || events[3].Type != session.EventInferenceFinish || len(p.Messages()) != 0 {
		t.Fatalf("progress events: %+v", events)
	}
	if events[2].Source == nil || events[3].Source == nil || *events[2].Source != *events[3].Source || m.env.AutoPilot.MissionState != setting.MissionDone {
		t.Fatal("progress did not join inference acceptance")
	}
	before := len(m.conv.Messages)
	m.handleAutopilotDecision(autopilotDecisionMsg{request: r, cont: cont, done: done, instruction: instruction})
	if len(m.conv.Messages) != before || m.env.AutoPilot.MissionState != setting.MissionDone {
		t.Fatal("completed request acted again")
	}
}

func TestAutopilotRecordingExpiredCurrentDecision(t *testing.T) {
	for _, done := range []bool{false, true} {
		t.Run(fmt.Sprint(done), func(t *testing.T) {
			m, owner := autopilotRecordingModel(t)
			r := m.newAutopilotRequest()
			ctx, cancel := context.WithCancel(r.ctx)
			cancel()
			r.ctx = ctx
			m.autopilotDeciding = true
			m.conv.Compact.Active = true
			m.env.AutoPilot.MissionState = setting.MissionRunning
			if cmd := m.handleAutopilotDecision(autopilotDecisionMsg{request: r, cont: !done, done: done, instruction: "expired"}); cmd != nil {
				t.Fatal("expired verdict deferred")
			}
			p := owner.Snapshot()
			if m.autopilotDeciding || m.autopilotContinuations != 0 || m.userInput.FullValue() != "" || m.env.AutoPilot.MissionState != setting.MissionPaused || len(p.Events()) != 1 {
				t.Fatal("expired success applied or indicator remained active")
			}
		})
	}
}

func TestAutopilotRecordingExpiredMissionResult(t *testing.T) {
	m, _ := autopilotRecordingModel(t)
	_, current := m.missionRefine("keep original draft")
	r := m.autopilotRequest
	ctx, cancel := context.WithCancel(r.ctx)
	cancel()
	r.ctx = ctx
	valid, err := current()
	if !valid || !errors.Is(err, context.Canceled) {
		t.Fatalf("current timeout was silently dropped: %v %v", valid, err)
	}
	_, stale := m.missionRefine("old")
	m.newAutopilotRequest()
	if valid, _ := stale(); valid {
		t.Fatal("old mission result accepted")
	}
}

func TestAutopilotRecordingStaleBeforeCompactionDeferral(t *testing.T) {
	m, _ := autopilotRecordingModel(t)
	old := m.newAutopilotRequest()
	successor := m.newAutopilotRequest()
	m.autopilotDeciding = true
	m.conv.Compact.Active = true
	cmd := m.handleAutopilotDecision(autopilotDecisionMsg{request: old, cont: true, instruction: "stale"})
	if cmd != nil || !m.autopilotDeciding || m.autopilotRequest != successor || m.userInput.FullValue() != "" {
		t.Fatal("stale request touched successor")
	}
}
