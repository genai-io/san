package session_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestStandaloneRequestThroughPublicAPI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	store, err := session.OpenStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s, err := store.Create(ctx, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	request := session.StandaloneRequest{System: "Judge this call", Messages: []ai.Message{
		{ID: "input", Role: ai.RoleUser, Content: ai.TextContent("read the file")},
	}}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.PutRequest(cancelled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request write: %v", err)
	}
	digest, err := s.PutRequest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	provider, model := "provider", "model"
	if err := s.Append(ctx, session.Event{Payload: session.InferenceStartPayload{
		Purpose: session.PurposeJudge, Request: session.EventConfig{Provider: &provider, Model: &model},
		Input: session.InferenceInput{Standalone: digest},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutRequest(ctx, request); !errors.Is(err, session.ErrClosed) {
		t.Fatalf("request write after Close: %v", err)
	}
	reopened, err := store.Open(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Request(ctx, digest)
	if err != nil || !reflect.DeepEqual(got, request) {
		t.Fatalf("request roundtrip: %+v %v", got, err)
	}
}
