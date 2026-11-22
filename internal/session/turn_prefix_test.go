package session

import (
	"errors"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestLastCompletedTurn(t *testing.T) {
	ctx := t.Context()
	s := createTyped(t, cleanStore(t), CreateOptions{ID: "turn-prefix"})
	start := func(purpose, provider, model string, request *ai.Request) *Inference {
		t.Helper()
		h, err := s.StartInference(ctx, InferenceOptions{Purpose: purpose, TurnID: "turn", Provider: provider, Model: ai.Model{ID: model}, Request: request})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	finish := func(h *Inference, stop ai.StopReason, callErr error, messages ...ai.Message) {
		t.Helper()
		var results []Event
		if len(messages) != 0 {
			results = append(results, Event{Payload: MessageAppendPayload{Messages: messages}})
		}
		if err := h.Finish(ctx, &ai.Response{StopReason: stop}, callErr, results...); err != nil {
			t.Fatal(err)
		}
	}
	find := func(provider, model string) (TurnPrefix, bool) {
		p := s.Snapshot()
		return p.LastCompletedTurn(provider, model)
	}
	user := eventText("user", ai.RoleUser, "question")
	call := ai.Message{ID: "call", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "read", Name: "Read", Input: `{}`})}}
	result := ai.Message{ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "read", ToolName: "Read", Content: ai.TextContent("file")})}}
	finish(start(PurposeTurn, "test", "model", &ai.Request{Messages: []ai.Message{user}}), ai.StopReason("tool_use"), nil, call)
	admitted := &ai.Request{System: "admitted instructions", Messages: []ai.Message{user, call, result}, Tools: []ai.Schema{{Name: "Read"}}, MaxTokens: 123}
	last := start(PurposeTurn, "test", "model", admitted)
	answer := ai.Message{ID: "answer", Role: ai.RoleAssistant, Content: ai.Content{ai.ThinkingBlock("reasoning", "signature"), ai.TextBlock("answer")}}
	more := eventText("more", ai.RoleAssistant, "more")
	finish(last, ai.StopEndTurn, nil, answer, more)
	finish(start(PurposeBtw, "test", "model", &ai.Request{}), ai.StopEndTurn, nil)
	prefix, ok := find("test", "model")
	if !ok {
		t.Fatal("no completed main turn after Aside")
	}
	for _, route := range [][2]string{{"other", "model"}, {"test", "other"}} {
		if _, ok := find(route[0], route[1]); ok {
			t.Fatalf("matched wrong route: %v", route)
		}
	}
	newer := start(PurposeTurn, "test", "model", &ai.Request{System: "new input"})
	if _, ok := find("test", "model"); ok {
		t.Fatal("fell back past an unfinished main inference")
	}
	finish(newer, ai.StopEndTurn, errors.New("failed"))
	if _, ok := find("test", "model"); ok {
		t.Fatal("fell back past a failed main inference")
	}
	finish(start(PurposeTurn, "test", "model", &ai.Request{}), ai.StopReason("length"), nil)
	if _, ok := find("test", "model"); ok {
		t.Fatal("fell back past a non-EndTurn finish")
	}
	other := &ai.Request{System: "different route"}
	finish(start(PurposeTurn, "other", "model", other), ai.StopEndTurn, nil)
	if _, ok := find("test", "model"); ok {
		t.Fatal("fell back past a completed main inference on another route")
	}
	noReply, ok := find("other", "model")
	if !ok {
		t.Fatal("missing output should not disqualify a completed turn")
	}
	if got, err := noReply.Request(ctx, s); err != nil || !sameJSON(got, other) {
		t.Fatalf("request without reply changed: %+v, %v", got, err)
	}
	want := *admitted
	want.Messages = append(want.Messages, answer, more)
	for i := 0; i < 2; i++ {
		got, err := prefix.Request(ctx, s)
		if err != nil || !sameJSON(got, &want) {
			t.Fatalf("captured request changed: %+v, %v", got, err)
		}
		got.Messages[len(got.Messages)-1].Content[0] = ai.TextBlock("caller mutation")
	}
	if got, err := s.InferenceRequest(ctx, last.Ref().Seq); err != nil || !sameJSON(got, admitted) {
		t.Fatalf("stored request changed: %+v, %v", got, err)
	}
}
