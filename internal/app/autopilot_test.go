package app

import (
	"iter"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"context"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/setting"
)

type autopilotStubProvider struct {
	content string
	// replies, when set, is served one entry per call so a test can stage a
	// failed attempt followed by a good one; the last entry repeats.
	replies []string

	driver *aitest.Driver
}

func (s *autopilotStubProvider) Client(string, map[string]string) (*ai.Client, error) {
	if s.driver == nil {
		s.driver = aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
			content := s.content
			if len(s.replies) > 0 {
				content = s.replies[min(s.driver.Calls()-1, len(s.replies)-1)]
			}
			return aitest.Says(content)(ctx, req)
		})
	}
	return s.driver.Client(), nil
}

// lastOptions is what the steer actually sent, for prompt assertions.
func (s *autopilotStubProvider) lastOptions() llm.CompletionOptions {
	req := s.driver.Last()
	msgs := make([]core.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, core.Message{Role: m.Role, Content: m.Content})
	}
	return llm.CompletionOptions{SystemPrompt: req.System, Messages: msgs}
}

func (s *autopilotStubProvider) calls() int {
	if s.driver == nil {
		return 0
	}
	return s.driver.Calls()
}

func (s *autopilotStubProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (s *autopilotStubProvider) Name() string                                        { return "stub" }

func TestAutopilotRecentTranscriptIncludesCompletionEvidence(t *testing.T) {
	messages := []core.ChatMessage{
		{Role: core.ChatUser, Content: core.FormatCompactSummary("created the package skeleton")},
		{Role: core.ChatAssistant, Content: "Run the tests."},
		{Role: core.ChatUser, Content: "ok", ToolResult: &core.ToolResult{
			ToolName: "Bash", Content: ai.TextContent("ok  github.com/genai-io/san/internal/app"), IsError: false,
		}},
	}

	got := autopilotRecentTranscript(messages, 3000)
	for _, want := range []string{"session summary:", "created the package skeleton", "tool Bash result:", "ok  github.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("recent transcript missing %q:\n%s", want, got)
		}
	}
}

func TestAutopilotRecentTranscriptSkipsEmptyCompactSummary(t *testing.T) {
	messages := []core.ChatMessage{
		{Role: core.ChatUser, Content: core.FormatCompactSummary("")},
		{Role: core.ChatAssistant, Content: "Working on it."},
	}

	got := autopilotRecentTranscript(messages, 3000)
	if strings.Contains(got, "Previous context:") {
		t.Errorf("empty compact summary leaked the sentinel marker:\n%s", got)
	}
	if strings.Contains(got, "session summary:") {
		t.Errorf("empty compact summary should be dropped, not emitted as a row:\n%s", got)
	}
}

func TestAutopilotDecideContinueRejectsContradictoryStates(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"continue", `{"continue":true,"done":false,"instruction":"Run the tests."}`, false},
		{"done", `{"continue":false,"done":true,"instruction":""}`, false},
		{"handback", `{"continue":false,"done":false,"instruction":""}`, false},
		{"continue and done", `{"continue":true,"done":true,"instruction":"Run tests."}`, true},
		{"continue without instruction", `{"continue":true,"done":false,"instruction":""}`, true},
		{"done with instruction", `{"continue":false,"done":true,"instruction":"Do more."}`, true},
		{"stop with instruction", `{"continue":false,"done":false,"instruction":"Do more."}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noSteerBackoff(t)
			_, _, _, err := autopilotDecideContinue(context.Background(), &autopilotStubProvider{content: tt.content}, "model", "system", "mission", "evidence", "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("autopilotDecideContinue() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// noSteerBackoff removes the retry sleep so a test that exercises the retry path
// doesn't wait out the real backoff.
func noSteerBackoff(t *testing.T) {
	t.Helper()
	previous := autopilotSteerBackoff
	autopilotSteerBackoff = 0
	t.Cleanup(func() { autopilotSteerBackoff = previous })
}

func TestAutopilotDecideContinueRetriesAnUnusableReply(t *testing.T) {
	noSteerBackoff(t)
	provider := &autopilotStubProvider{replies: []string{
		"I think we should keep going!", // prose, not JSON — a transient model slip
		`{"continue":true,"done":false,"instruction":"Run the tests."}`,
	}}

	cont, done, instruction, err := autopilotDecideContinue(context.Background(), provider, "model", "system", "mission", "evidence", "")
	if err != nil {
		t.Fatalf("autopilotDecideContinue() err = %v, want nil after retry", err)
	}
	if !cont || done || instruction != "Run the tests." {
		t.Fatalf("got cont=%v done=%v instruction=%q, want the retried decision", cont, done, instruction)
	}
	if provider.calls() != 2 {
		t.Errorf("provider calls = %d, want 2 (one failure, one retry)", provider.calls())
	}
}

func TestAutopilotDecideContinueGivesUpAfterAttemptsAreSpent(t *testing.T) {
	noSteerBackoff(t)
	provider := &autopilotStubProvider{content: "still not JSON"}

	if _, _, _, err := autopilotDecideContinue(context.Background(), provider, "model", "system", "mission", "evidence", ""); err == nil {
		t.Fatal("autopilotDecideContinue() err = nil, want the last parse failure")
	}
	if provider.calls() != autopilotSteerAttempts {
		t.Errorf("provider calls = %d, want %d", provider.calls(), autopilotSteerAttempts)
	}
}

func TestAutopilotDecideContinueCarriesTheSituation(t *testing.T) {
	noSteerBackoff(t)
	provider := &autopilotStubProvider{content: `{"continue":false,"done":false,"instruction":""}`}

	if _, _, _, err := autopilotDecideContinue(context.Background(), provider, "model", "system", "", "evidence",
		"the previous turn hit its step limit"); err != nil {
		t.Fatalf("autopilotDecideContinue() err = %v", err)
	}
	sent := provider.lastOptions().Messages[0].Text()
	if !strings.Contains(sent, "the previous turn hit its step limit") {
		t.Errorf("prompt missing the stop situation:\n%s", sent)
	}
}

// A finished goal rewinds to the configuration /goal found — otherwise the
// driving set stays on behind the user — and stays on record as done.
func TestGoalFinishRestoresTheConfigItFound(t *testing.T) {
	m := &model{autopilot: &atomic.Pointer[autopilotRuntime]{}}
	suggestOn := true
	configured := setting.AutoPilotSettings{
		Steers:           setting.SteerSettings{Suggest: &suggestOn},
		MaxContinuations: 5,
	}
	// What startGoal leaves behind: the pre-goal snapshot plus the driving set.
	m.env.AutoPilot = configured.Clone()
	before := configured.Clone()
	m.beforeGoal = &before
	m.env.AutoPilot.Mission = "ship it"
	m.env.AutoPilot.MissionState = setting.MissionRunning
	m.env.AutoPilot.EngageDriving()

	m.finishMission()
	got := m.env.AutoPilot
	if got.Mission != "ship it" || got.MissionState != setting.MissionDone {
		t.Errorf("mission = %q (%s), want it kept as done", got.Mission, got.MissionState)
	}
	got.Mission, got.MissionState = "", ""
	if !got.Equal(configured) {
		t.Errorf("finish left %+v, want the pre-goal config %+v", got, configured)
	}
	if m.beforeGoal != nil {
		t.Error("the pre-goal snapshot outlived the goal")
	}
}

// Without a goal, finishing changes only the state: the switches keep the
// user's settings, and the done mission stops steering decisions.
func TestFinishMissionKeepsSwitches(t *testing.T) {
	m := &model{autopilot: &atomic.Pointer[autopilotRuntime]{}}
	m.userInput.PromptSuggestion.Text = "stale mission hint"
	suggestOn := true
	m.env.AutoPilot = setting.AutoPilotSettings{
		Mission: "ship it", MissionState: setting.MissionRunning,
		Steers: setting.SteerSettings{BashPrompt: true, Suggest: &suggestOn, TurnEnd: true},
	}

	m.finishMission()
	ar := m.env.AutoPilot
	if !ar.Steers.BashPrompt || !ar.Steers.SuggestOn() || !ar.Steers.TurnEnd {
		t.Errorf("finish changed the user's switches: %+v", ar.Steers)
	}
	if ar.ActiveMission() != "" {
		t.Error("a done mission still steers decisions")
	}
	if m.userInput.PromptSuggestion.Text != "" {
		t.Error("finish left a stale mission suggestion on screen")
	}
}

// Only a running mission is driven: a done or paused mission's turn end waits
// for the human, and a running one the human steps into pauses.
func TestMissionLifecycleGatesContinuation(t *testing.T) {
	m := newSuggestionModel(t, true, setting.ModeAutoPilot)
	m.env.AutoPilot.Mission = "ship it"
	m.env.AutoPilot.Steers.TurnEnd = true
	end := core.Result{StopReason: core.StopEndTurn}

	for _, st := range []setting.MissionState{"", setting.MissionPaused, setting.MissionDone} {
		m.env.AutoPilot.MissionState = st
		if cmd := m.autopilotContinueCmd(end); cmd != nil {
			t.Errorf("%s mission was driven at turn end", st)
		}
	}

	m.env.AutoPilot.MissionState = setting.MissionRunning
	if cmd := m.autopilotContinueCmd(end); cmd == nil {
		t.Fatal("running mission was not driven at turn end")
	}

	m.autopilotDeciding = false
	m.pauseMission()
	if m.env.AutoPilot.MissionState != setting.MissionPaused {
		t.Fatalf("state = %s, want paused after the human stepped in", m.env.AutoPilot.MissionState)
	}
}

// Saving the panel owns the settings, not where the run stands: a changed
// mission starts over as ready, an unchanged one keeps its live state.
func TestApplyAutopilotConfigMissionState(t *testing.T) {
	m := &model{autopilot: &atomic.Pointer[autopilotRuntime]{}}
	m.env.AutoPilot = setting.AutoPilotSettings{Mission: "ship it", MissionState: setting.MissionPaused}

	m.applyAutopilotConfig(setting.AutoPilotSettings{Mission: "ship it"})
	if got := m.env.AutoPilot.MissionState; got != setting.MissionPaused {
		t.Errorf("unchanged mission: state = %s, want the live paused", got)
	}

	m.env.AutoPilot.MissionState = setting.MissionDone
	m.applyAutopilotConfig(setting.AutoPilotSettings{Mission: "ship the docs"})
	if got := m.env.AutoPilot.MissionState; got != "" {
		t.Errorf("changed mission: state = %s, want not started", got)
	}

	m.applyAutopilotConfig(setting.AutoPilotSettings{})
	if got := m.env.AutoPilot.MissionState; got != "" {
		t.Errorf("cleared mission: state = %s, want none", got)
	}
}

// newSuggestionModel builds a model wired far enough for the input hint to
// fire: a stub provider, a conversation to predict from, and the mode entered
// the way the app enters it (the steer gates read the permission posture, not
// the raw field).
func newSuggestionModel(t *testing.T, suggest bool, mode setting.OperationMode) *model {
	m := &model{autopilot: &atomic.Pointer[autopilotRuntime]{}}
	m.services.LLM = &llm.Conn{}
	m.env.LLMProvider = &autopilotStubProvider{}
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.env.OperationMode = mode
	m.env.ApplyModePermissions(t.TempDir())
	m.env.AutoPilot.Steers.Suggest = &suggest
	m.conv.Messages = []core.ChatMessage{
		{Role: core.ChatAssistant, Content: "Done."},
		{Role: core.ChatUser, Content: "Next."},
		{Role: core.ChatAssistant, Content: "Also done."},
	}
	return m
}

// The Suggest steer is the input hint's feature switch in every mode: when
// explicitly off the hint never fires — including outside AutoPilot, where
// generic prediction used to be unconditional.
func TestPromptSuggestionGatedOnSuggestSteerInEveryMode(t *testing.T) {
	if cmd := newSuggestionModel(t, false, setting.ModeNormal).startPromptSuggestion(); cmd != nil {
		t.Error("suggest steer off: the hint still fired outside AutoPilot")
	}
	if cmd := newSuggestionModel(t, false, setting.ModeAutoPilot).startPromptSuggestion(); cmd != nil {
		t.Error("suggest steer off: the hint still fired in AutoPilot mode")
	}
	if cmd := newSuggestionModel(t, true, setting.ModeNormal).startPromptSuggestion(); cmd == nil {
		t.Error("suggest steer on: no hint outside AutoPilot")
	}
}

// Shift+Tab can now land on AutoPilot mid-turn. The opening proposal is ghost
// text for a textarea the user cannot type into yet, so it must not cost an LLM
// call — OnTurnEnd re-arms the hint once the turn is over.
func TestAutopilotModeSettledSkipsSuggestionMidTurn(t *testing.T) {
	m := newSuggestionModel(t, true, setting.ModeAutoPilot)

	if cmd := m.handleAutopilotModeSettled(); cmd == nil {
		t.Fatal("settling on AutoPilot while idle did not start the proposal")
	}

	m.conv.Stream.Active = true
	if cmd := m.handleAutopilotModeSettled(); cmd != nil {
		t.Fatal("settling on AutoPilot mid-turn started the proposal anyway")
	}
}

// A ready mission shows a start offer in the idle composer instead of a
// proposal; typing steps it aside and esc dismisses it.
func TestMissionOfferInComposer(t *testing.T) {
	m := newSuggestionModel(t, true, setting.ModeAutoPilot)
	m.userInput = input.New("", 80, nil, input.SelectorDeps{})
	m.env.AutoPilot.Mission = "ship the release"
	m.env.AutoPilot.MissionState = "" // not started

	if !m.missionOfferVisible() {
		t.Fatal("a ready mission did not offer to start")
	}
	if cmd := m.handleAutopilotModeSettled(); cmd != nil {
		t.Fatal("settling fetched a proposal while the start offer owns the composer")
	}

	m.userInput.Textarea.SetValue("run the tests first")
	if m.missionOfferVisible() {
		t.Fatal("start offer still showing over typed input")
	}
	m.userInput.Textarea.SetValue("")

	if _, ok := m.handleTextareaShortcut(tea.KeyPressMsg{Code: tea.KeyEscape}); !ok || m.missionOfferVisible() {
		t.Fatal("esc did not dismiss the start offer")
	}
	m.autopilotOfferDismissed = false // OnTurnEnd re-arms it

	m.env.AutoPilot.MissionState = setting.MissionDone
	if m.missionOfferVisible() {
		t.Fatal("a done mission still offers to start")
	}
}

func TestAutopilotSaveWithSuggestOffClearsPromptSuggestion(t *testing.T) {
	off := false
	m := &model{autopilot: &atomic.Pointer[autopilotRuntime]{}}
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.userInput.PromptSuggestion.Text = "stale hint"

	_, _ = m.Update(input.AutopilotSavedMsg{Config: setting.AutoPilotSettings{
		Steers: setting.SteerSettings{Suggest: &off},
	}})

	if m.userInput.PromptSuggestion.Text != "" {
		t.Fatal("saving Suggest off left stale prompt suggestion text")
	}
}

func TestAutopilotStopEvidence(t *testing.T) {
	resumable := map[core.StopReason]bool{
		core.StopEndTurn:   true,
		core.StopMaxSteps:  true,
		core.StopTruncated: true,
		core.StopCanceled:  false, // the human took the helm
		core.StopHook:      false, // a configured halt
	}
	for reason, want := range resumable {
		got, situation := autopilotStopEvidence(reason)
		if got != want {
			t.Errorf("autopilotStopEvidence(%q) resumable = %v, want %v", reason, got, want)
		}
		// Every resumable stop but a clean end_turn must explain itself, or the
		// copilot sees an inexplicably truncated transcript.
		if wantSituation := want && reason != core.StopEndTurn; wantSituation != (situation != "") {
			t.Errorf("autopilotStopEvidence(%q) situation = %q, want explained = %v", reason, situation, wantSituation)
		}
	}
}
