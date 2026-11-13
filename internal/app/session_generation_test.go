package app

import (
	"errors"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestSavedSessionMaxOutputDrivesActualRequest(t *testing.T) {
	m, _ := resumedModel(t, nil)
	cap := 321
	if err := m.services.Session.Append(t.Context(), session.Event{Payload: session.SessionUpdatePayload{Config: &session.ConfigPatch{GenerationOptions: session.SetField(session.GenerationOptions{Version: 1, MaxTokens: &cap})}}}); err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Says("ok"))
	m.env.LLMProvider = &mainSessionProvider{client: driver.Client()}
	if m.sessionMaxOutput() != cap {
		t.Fatal("saved cap ignored")
	}
	_, err := m.buildLLMClient().Complete(t.Context(), "", []core.Message{core.UserMessage("hello", nil)}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if driver.Last().MaxTokens != cap {
		t.Fatalf("actual max_tokens=%d", driver.Last().MaxTokens)
	}
}

func TestExplicitSessionMaxOutputPatchPreservesGeneration(t *testing.T) {
	for _, kind := range []string{"apply", "stale-session", "stale-model", "stale-provider", "same-id-reopen", "closed"} {
		t.Run(kind, func(t *testing.T) {
			m, store := resumedModel(t, nil)
			cap, temp := 321, 0.4
			original := session.GenerationOptions{Version: 1, MaxTokens: &cap, Temperature: &temp, Effort: ai.EffortHigh, StopSequences: []string{"stop"}, ToolChoice: ai.ToolChoiceNone}
			if err := m.services.Session.Append(t.Context(), session.Event{Payload: session.SessionUpdatePayload{Config: &session.ConfigPatch{GenerationOptions: session.SetField(original)}}}); err != nil {
				t.Fatal(err)
			}
			m.env.CurrentModel = &llm.CurrentModelInfo{Provider: llm.OpenAI, ModelID: "m"}
			msg := input.ContextLimitUpdatedMsg{Session: m.services.Session, ModelID: "m", Provider: string(llm.OpenAI), MaxTokens: 654}
			switch kind {
			case "stale-session":
				msg.Session = nil
			case "same-id-reopen":
				originalOwner := m.services.Session
				if err := originalOwner.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := store.Open(t.Context(), originalOwner.ID())
				if err != nil {
					t.Fatal(err)
				}
				m.services.Session = reopened
				if reopened == msg.Session || reopened.ID() != msg.Session.ID() {
					t.Fatal("fixture did not replace the owner of the same session ID")
				}
			case "stale-model":
				msg.ModelID = "other"
			case "stale-provider":
				msg.Provider = "other"
			case "closed":
				if err := m.services.Session.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err := m.applyContextLimitUpdate(msg)
			if kind == "closed" {
				if !errors.Is(err, session.ErrClosed) {
					t.Fatalf("err=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			snapshot := m.services.Session.Snapshot()
			got := snapshot.Config().GenerationOptions
			want := original
			if kind == "apply" {
				want.MaxTokens = &msg.MaxTokens
			}
			if !reflect.DeepEqual(got, &want) {
				t.Fatalf("generation=%+v want=%+v", got, want)
			}
		})
	}
}
