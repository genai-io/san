package agent

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type compactRecordingProvider struct{ driver *aitest.Driver }

func (p compactRecordingProvider) Name() string { return "compact-test" }
func (p compactRecordingProvider) Client(string, map[string]string) (*ai.Client, error) {
	return ai.NewClientWithDriver(p.driver, ai.Model{ID: "model", ContextWindow: 1000, MaxOutput: 64}), nil
}
func (p compactRecordingProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "model", ContextWindow: 1000, MaxOutput: 64}}, nil
}

func TestBuiltAgentRecordsAutomaticCompaction(t *testing.T) {
	for _, mode := range []string{"accepted", "closed", "empty", "provider-error"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			store, err := session.OpenStore(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			owner, err := store.Create(t.Context(), session.CreateOptions{ID: "auto-compact"})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			messages := []core.Message{core.UserMessage("first", nil), core.UserMessage("second", nil), core.UserMessage(strings.Repeat("work ", 2000), nil)}
			for i := range messages {
				messages[i].ID = core.NewMessageID()
			}
			if err := owner.Append(t.Context(), session.Event{Payload: session.MessageAppendPayload{Messages: messages}}); err != nil {
				t.Fatal(err)
			}
			first := func(ctx context.Context, r *ai.Request) iter.Seq2[ai.Delta, error] {
				text := "condensed"
				if mode == "empty" {
					text = " "
				}
				var callErr error
				if mode == "provider-error" {
					callErr = errors.New("provider failed")
				}
				if mode == "closed" {
					if err := owner.Close(); err != nil {
						t.Error(err)
					}
				}
				return aitest.Replies(ai.Response{Content: ai.TextContent(text), Usage: ai.Usage{Input: 17, Output: 4}, Err: callErr})(ctx, r)
			}
			driver := aitest.New(first, aitest.Says("done"))
			boundaries := 0
			ag, gate, err := buildAgentWithOutbox(BuildParams{Provider: compactRecordingProvider{driver}, ModelID: "model", MaxTokens: 64,
				PermissionRules: func(string, map[string]any) PermDecisionResult {
					return PermDecisionResult{Decision: perm.Reject, By: session.ByConfig}
				},
				CWD: t.TempDir(), Recording: owner, OnEvent: func(event core.Event) {
					if info, ok := event.(core.Compacted); ok {
						boundaries++
						p := owner.Snapshot()
						active := p.ActiveMessages()
						if len(active) != 1 || active[0].ID != info.SummaryMessageID || active[0].Text() != core.FormatCompactSummary(info.Summary) {
							t.Error("published compaction before exact native commit")
						}
					}
				}}, -1)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Close()
			ag.SetMessages(messages)
			_, err = ag.ThinkAct(t.Context())
			if mode == "closed" {
				if !errors.Is(err, session.ErrClosed) || driver.Calls() != 1 || len(ag.Messages()) != len(messages) {
					t.Fatalf("failed persistence crossed compaction: err=%v calls=%d", err, driver.Calls())
				}
				return
			}
			if err != nil || driver.Calls() != 2 {
				t.Fatalf("err=%v model calls=%d", err, driver.Calls())
			}
			want := 0
			if mode == "accepted" {
				want = 1
			}
			if boundaries != want {
				t.Fatalf("boundaries=%d want=%d", boundaries, want)
			}
			p := owner.Snapshot()
			events := p.Events()
			compactions := 0
			for _, event := range events {
				finish, ok := event.Payload.(*session.InferenceFinishPayload)
				if !ok || event.Source == nil {
					continue
				}
				start, ok := events[event.Source.Seq-1].Payload.(*session.InferenceStartPayload)
				if !ok || start.Purpose != session.PurposeCompaction {
					continue
				}
				compactions++
				if finish.Usage == nil || finish.Usage.Input != 17 || finish.Usage.Output != 4 {
					t.Fatal("compaction lost usage")
				}
			}
			if compactions != 1 {
				t.Fatalf("compaction attempts = %d, want 1", compactions)
			}
			if len(p.Unfinished().Inferences) != 0 {
				t.Fatal("compaction attempt left unfinished")
			}
		})
	}
}
