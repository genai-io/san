package automemory

import (
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestMaintainReusesMatchingMainRequestPrefix(t *testing.T) {
	ctx := t.Context()
	store, err := session.OpenStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.Create(ctx, session.CreateOptions{ID: "maintain"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	prefix := &ai.Request{
		System:   "Stable main system prompt",
		Messages: []core.Message{core.UserMessage("first question", nil)},
		Tools:    []ai.Schema{{Name: "search", Definition: map[string]any{"type": "object"}}},
	}
	inf, err := owner.StartInference(ctx, session.InferenceOptions{Purpose: session.PurposeTurn, TurnID: "first", Provider: "fake", Model: ai.Model{ID: "main"}, Request: prefix})
	if err != nil {
		t.Fatal(err)
	}
	if err := inf.Finish(ctx, &ai.Response{StopReason: ai.StopEndTurn}, nil); err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Says(`{"action":"none"}`), aitest.Says(`{"action":"none"}`))
	m := Maintainer{Provider: fakeProvider{driver}, Model: "main", Recording: owner}
	stores := system.MemoryStores{Project: t.TempDir()}
	req := request(t, "remember: Prefer table tests.")
	if _, err := m.Run(ctx, stores, req); err != nil {
		t.Fatal(err)
	}
	got := driver.Last()
	if got.System != prefix.System || !reflect.DeepEqual(got.Tools, prefix.Tools) || got.ToolChoice != ai.ToolChoiceNone || len(got.Messages) != 2 || !reflect.DeepEqual(got.Messages[:1], prefix.Messages) {
		t.Fatalf("maintenance changed the main prefix or did not disable tools: %+v", got)
	}
	if !strings.Contains(got.Messages[1].Text(), "Prefer table tests.") || !strings.Contains(got.Messages[1].Text(), "not earlier conversation") {
		t.Fatalf("missing maintenance suffix: %q", got.Messages[1].Text())
	}
	m.Model = "other"
	if _, err := m.Run(ctx, stores, req); err != nil {
		t.Fatal(err)
	}
	if got := driver.Last(); got.System == prefix.System || len(got.Messages) != 1 || len(got.Tools) != 0 {
		t.Fatalf("maintenance shared a prefix with another route: %+v", got)
	}
	p := owner.Snapshot()
	if len(p.Messages()) != 0 {
		t.Fatal("maintenance added messages to main history")
	}
}
