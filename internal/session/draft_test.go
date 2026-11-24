package session

import (
	"errors"
	"os"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestDraftDoesNotPersistWithoutActivity(t *testing.T) {
	store := cleanStore(t)
	draft, err := store.NewDraft(t.Context(), CreateOptions{ID: "blank"})
	if err != nil {
		t.Fatal(err)
	}
	if err := draft.Append(t.Context(), Event{Payload: AnnotationAddPayload{Text: "Startup notice"}},
		Event{Payload: SessionUpdatePayload{Config: &ConfigPatch{Model: SetField("selected-model")}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(draft.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unused draft has a transcript: %v", err)
	}
	if err := draft.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := store.List(t.Context(), ListQuery{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("unused draft persisted: %+v, %v", page.Items, err)
	}
}

func TestDraftPublishesFirstAcceptedInputWithPendingState(t *testing.T) {
	store := cleanStore(t)
	draft, err := store.NewDraft(t.Context(), CreateOptions{ID: "used"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = draft.Close() })
	if err := draft.Append(t.Context(), Event{Payload: SessionUpdatePayload{Config: &ConfigPatch{Model: SetField("selected-model")}}}); err != nil {
		t.Fatal(err)
	}
	message := ai.Message{ID: "first", Role: ai.RoleUser, Content: ai.TextContent("Hello")}
	if err := draft.AcceptInput(t.Context(), message, nil); err != nil {
		t.Fatal(err)
	}
	if err := draft.Append(t.Context(), Event{Payload: AnnotationAddPayload{Text: "After publication"}}); err != nil {
		t.Fatal(err)
	}
	if err := draft.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), draft.ID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	p := reopened.Snapshot()
	if cfg, messages := p.Config(), p.Messages(); cfg.Model == nil || *cfg.Model != "selected-model" || len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("failed to restore draft state and first input: %+v, %+v", cfg, messages)
	}
}

func TestDraftDoesNotReplaceAnExistingSession(t *testing.T) {
	store := cleanStore(t)
	original := createTyped(t, store, CreateOptions{ID: "same", Config: &EventConfig{Model: new("original")}})
	draft, err := store.NewDraft(t.Context(), CreateOptions{ID: original.ID()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = draft.Close() })
	if err := draft.Save(t.Context()); err == nil {
		t.Fatal("draft replaced an existing session")
	}
	if err := draft.Close(); err != nil {
		t.Fatal(err)
	}
	p, err := store.Read(t.Context(), original.ID())
	if err != nil {
		t.Fatal(err)
	}
	if model := p.Config().Model; model == nil || *model != "original" {
		t.Fatalf("publication changed the existing session: %+v", p.Config())
	}
}
