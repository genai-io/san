package app

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

const draftBlueprint = "---\nname: inspect\ndescription: Inspect only\nmax_parallel: 1\n---\n```mermaid\nflowchart LR\n inspect\n```\n\n## inspect\nmode: explore\n\nInspect {{input}}.\n"

func draftTestModel(t *testing.T) *model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	m := newRecapModel(t)
	m.services.Session, m.services.sessionID = owner, owner.ID()
	return m
}

func draftRequest(t *testing.T, m *model, goal string) input.FlowDraftRequestMsg {
	t.Helper()
	return m.userInput.Flow.OpenGeneration(nil, goal, 100, 35)().(input.FlowDraftRequestMsg)
}

func draftResult(t *testing.T, cmd tea.Cmd) flowDraftedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no drafting command")
	}
	result := cmd()
	if batch, ok := result.(tea.BatchMsg); ok {
		// The last command is the completion; the optional first is a spinner tick.
		result = batch[len(batch)-1]()
	}
	msg, ok := result.(flowDraftedMsg)
	if !ok {
		t.Fatalf("result type %T", result)
	}
	return msg
}

func TestFlowDraftOneShotReviewAndRecording(t *testing.T) {
	for _, source := range []string{draftBlueprint, "broken blueprint: keep me for editing"} {
		t.Run(source[:5], func(t *testing.T) {
			m := draftTestModel(t)
			goal := "Inspect \"quotes\" and 'apostrophes'\nexactly"
			m.services.Agent.SetMessages([]core.Message{core.UserMessage("existing context", nil)})
			calls := 0
			driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				calls++
				if len(req.Tools) != 0 {
					t.Error("draft advertised tools")
				}
				last := req.Messages[len(req.Messages)-1].Content.Text()
				if !strings.HasSuffix(last, goal) || !strings.Contains(last, flowAuthoringInstructions) {
					t.Errorf("goal/instructions changed: %q", last)
				}
				return aitest.Says(source)(ctx, req)
			})
			m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
			request := draftRequest(t, m, goal)
			cmd := m.generateFlow(request)
			if m.flowDraft == nil || !m.needsSpinner() {
				t.Fatal("pending draft not tracked")
			}
			if m.generateFlow(request) != nil {
				t.Fatal("duplicate generation dispatched")
			}
			result := draftResult(t, cmd)
			if !m.userInput.Flow.IsGenerating(request.Generation) {
				t.Fatal("UI changed before result acceptance")
			}
			if result.err != nil {
				t.Fatal(result.err)
			}
			m.onFlowDrafted(result)
			if calls != 1 || m.flowDraft != nil || m.userInput.Flow.IsGenerating(request.Generation) {
				t.Fatal("draft lifecycle did not settle")
			}
			rendered := m.userInput.Flow.Render()
			if !strings.Contains(rendered, "Review") && !strings.Contains(rendered, "review") {
				t.Fatalf("not in Review: %s", rendered)
			}
			if strings.HasPrefix(source, "broken") {
				m.userInput.Flow.HandleKeypress(tea.KeyPressMsg{Code: 'e', Text: "e"})
				if edited := m.userInput.Flow.Render(); !strings.Contains(edited, "broken blueprint") {
					t.Fatalf("malformed source lost: %s", edited)
				}
			}
			if messages := m.services.Agent.Messages(); len(messages) != 1 || messages[0].Content.Text() != "existing context" {
				t.Fatal("authoring leaked into main chain")
			}
			p := m.services.Session.Snapshot()
			if len(p.Messages()) != 0 || len(p.Unfinished().Inferences) != 0 {
				t.Fatal("ordinary messages or unfinished recording")
			}
			events := p.Events()
			if len(events) != 3 || events[1].Payload.(*session.InferenceStartPayload).Purpose != session.PurposeFlow || events[2].Type != session.EventInferenceFinish {
				t.Fatalf("wrong recording lifecycle: %+v", events)
			}
			// nil FlowService would panic if the host attempted automatic save or run.
		})
	}
}

func TestFlowDraftUsesSharedAnimation(t *testing.T) {
	m := draftTestModel(t)
	m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(aitest.New(aitest.Says(draftBlueprint)), ai.Model{ID: "m"})}
	request := draftRequest(t, m, "Review the parser")
	completion := m.generateFlow(request)
	before := m.userInput.Flow.Render()
	_, tick := m.Update(m.conv.Spinner.Tick())
	if tick == nil || m.userInput.Flow.Render() == before {
		t.Fatal("shared animation tick did not update the drafting overlay")
	}
	m.Update(draftResult(t, completion))
	if m.flowDraft != nil || !strings.Contains(m.userInput.Flow.Render(), "Unsaved") {
		t.Fatal("completion did not replace progress with unsaved Review")
	}
}

func TestFlowDraftFailures(t *testing.T) {
	for _, kind := range []string{"empty", "tool", "provider"} {
		t.Run(kind, func(t *testing.T) {
			m := draftTestModel(t)
			driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				switch kind {
				case "tool":
					return aitest.Asks(ai.ToolCall{ID: "write", Name: "Write", Input: `{}`})(ctx, req)
				case "provider":
					return func(yield func(ai.Delta, error) bool) { yield(ai.Delta{}, errors.New("provider failed")) }
				default:
					return aitest.Says("   ")(ctx, req)
				}
			})
			m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
			request := draftRequest(t, m, "draft")
			result := draftResult(t, m.generateFlow(request))
			if result.err == nil {
				t.Fatal("expected drafting failure")
			}
			m.onFlowDrafted(result)
			if m.flowDraft != nil || m.userInput.Flow.IsGenerating(request.Generation) {
				t.Fatal("failed request still spinning")
			}
			p := m.services.Session.Snapshot()
			if len(p.Unfinished().Inferences) != 0 {
				t.Fatal("failed recording left open")
			}
		})
	}
}

func TestFlowDraftRejectsAbnormalStops(t *testing.T) {
	for _, reason := range []ai.StopReason{ai.StopMaxTokens, ai.StopRefusal, ai.StopSequence, ai.StopToolUse, ai.StopAborted, ai.StopError, "unexpected"} {
		t.Run(string(reason), func(t *testing.T) {
			m := draftTestModel(t)
			// Even parseable source is not a completed draft after an abnormal stop.
			driver := aitest.New(aitest.Stops(reason, draftBlueprint), aitest.Says(draftBlueprint))
			m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
			request := draftRequest(t, m, "draft")
			result := draftResult(t, m.generateFlow(request))
			if result.err == nil || !strings.Contains(result.err.Error(), string(reason)) {
				t.Fatalf("stop %q accepted as a completed draft: %v", reason, result.err)
			}
			if result.source != "" {
				t.Fatal("abnormally stopped source offered as a completed draft")
			}
			m.onFlowDrafted(result)
			if m.flowDraft != nil || m.userInput.Flow.IsGenerating(request.Generation) {
				t.Fatal("failed request still spinning")
			}
			rendered := m.userInput.Flow.Render()
			if !strings.Contains(rendered, string(reason)) || !strings.Contains(rendered, "No draft was created") || !strings.Contains(rendered, "Enter retry") {
				t.Fatalf("abnormal stop not visibly reported: %s", rendered)
			}
			p := m.services.Session.Snapshot()
			events := p.Events()
			if len(p.Unfinished().Inferences) != 0 || len(p.Messages()) != 0 || len(events) != 3 {
				t.Fatal("failed recording was not closed without publishing messages")
			}
			finish, ok := events[2].Payload.(*session.InferenceFinishPayload)
			if !ok || finish.StopReason != string(reason) {
				t.Fatalf("abnormal stop not preserved in recording: %+v", events[2])
			}
			if driver.Calls() != 1 {
				t.Fatal("abnormal stop automatically retried")
			}
			retry := m.userInput.Flow.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})().(input.FlowDraftRequestMsg)
			if retry.Generation == request.Generation {
				t.Fatal("explicit retry reused failed generation")
			}
			retried := draftResult(t, m.generateFlow(retry))
			if retried.err != nil {
				t.Fatal(retried.err)
			}
			m.onFlowDrafted(retried)
			if driver.Calls() != 2 || !strings.Contains(m.userInput.Flow.Render(), "Unsaved") {
				t.Fatal("explicit retry did not open unsaved Review")
			}
		})
	}
}

func TestFlowDraftMissingProvider(t *testing.T) {
	m := draftTestModel(t)
	m.env.LLMProvider = nil
	request := draftRequest(t, m, "draft")
	p := m.services.Session.Snapshot()
	before := len(p.Events())
	if m.generateFlow(request) != nil || m.flowDraft != nil || m.userInput.Flow.IsGenerating(request.Generation) {
		t.Fatal("missing provider started request")
	}
	p = m.services.Session.Snapshot()
	if len(p.Events()) != before {
		t.Fatal("missing provider created recording")
	}
}

func TestFlowDraftCancellationFences(t *testing.T) {
	for _, fence := range []string{"close", "session", "replacement"} {
		t.Run(fence, func(t *testing.T) {
			m := draftTestModel(t)
			owner := m.services.Session
			entered := make(chan struct{})
			driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				close(entered)
				return aitest.Hangs()(ctx, req)
			})
			m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
			request := draftRequest(t, m, "old")
			cmd := m.generateFlow(request)
			done := make(chan flowDraftedMsg, 1)
			go func() { done <- draftResult(t, cmd) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("provider not entered")
			}
			var next input.FlowDraftRequestMsg
			switch fence {
			case "close":
				m.userInput.Flow.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
			case "session":
				m.services.Session = nil
			case "replacement":
				next = draftRequest(t, m, "new")
			}
			m.syncFlowDraft()
			if m.flowDraft != nil {
				t.Fatal("stale request retained")
			}
			var result flowDraftedMsg
			select {
			case result = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("request not cancelled")
			}
			m.onFlowDrafted(result)
			if fence == "replacement" && !m.userInput.Flow.IsGenerating(next.Generation) {
				t.Fatal("old result changed replacement UI")
			}
			p := owner.Snapshot()
			if len(p.Unfinished().Inferences) != 0 {
				t.Fatal("original recording remained open")
			}
		})
	}
}

func TestFlowDraftQueuedResultCannotReplaceNewRequest(t *testing.T) {
	m := draftTestModel(t)
	m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		return aitest.Says(draftBlueprint)(ctx, req)
	}), ai.Model{ID: "m"})}
	old := draftRequest(t, m, "old")
	result := draftResult(t, m.generateFlow(old))
	if result.err != nil {
		t.Fatal(result.err)
	}
	next := draftRequest(t, m, "replacement")
	m.syncFlowDraft()
	replacementCmd := m.generateFlow(next)
	replacement := m.flowDraft
	if replacement == nil {
		t.Fatal("replacement not started")
	}
	m.onFlowDrafted(result)
	if m.flowDraft != replacement || !m.userInput.Flow.IsGenerating(next.Generation) {
		t.Fatal("queued stale response changed replacement")
	}
	final := draftResult(t, replacementCmd)
	m.onFlowDrafted(final)
	p := m.services.Session.Snapshot()
	if len(p.Unfinished().Inferences) != 0 {
		t.Fatal("recording remained open")
	}
}

func TestUnwrapFlowDraft(t *testing.T) {
	for _, wrapper := range []string{"", "```markdown", "````md", "`````"} {
		source := strings.TrimSpace(draftBlueprint)
		wrapped := source
		if wrapper != "" {
			n := len(wrapper) - len(strings.TrimLeft(wrapper, "`"))
			wrapped = wrapper + "\n" + source + "\n" + strings.Repeat("`", n)
		}
		if got := unwrapFlowDraft(wrapped); got != source {
			t.Fatalf("wrapper %q: %q", wrapper, got)
		}
	}
	mermaid := "```mermaid\nflowchart LR\n a\n```"
	if unwrapFlowDraft(mermaid) != mermaid {
		t.Fatal("inner mermaid fence removed")
	}
}
