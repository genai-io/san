package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPParallelQuestions(t *testing.T) {
	question := ai.ToolCall{ID: "same-tool-id", Name: "AskUserQuestion", Input: `{"options":["A","B"]}`}
	driver := aitest.New(aitest.Asks(question), aitest.Asks(question), aitest.Says("done"), aitest.Says("done"))
	entered := make(chan protocol.SessionID, 2)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := newACPFixtureOptions(t, driver, nil, acpWriteTimeout, acpFormInit(), func(ctx context.Context, req protocol.CreateElicitationRequest) (protocol.CreateElicitationResponse, error) {
		id := req.Value.(protocol.CreateElicitationRequestFormVariant).SessionID.Value
		entered <- id
		select {
		case <-release:
		case <-ctx.Done():
			return protocol.CreateElicitationResponse{}, ctx.Err()
		}
		content, _ := json.Marshal(map[string]string{"q0": "other", "q0_text": string(id)})
		return acpAccepted(string(content)), nil
	})
	first, second := f.session(t, t.TempDir()), f.session(t, t.TempDir())
	done := make(chan error, 2)
	for _, id := range []protocol.SessionID{first, second} {
		go func() { _, err := f.prompt(id, "ask"); done <- err }()
	}
	seen := map[protocol.SessionID]bool{}
	for range 2 {
		select {
		case id := <-entered:
			seen[id] = true
		case <-f.ctx.Done():
			t.Fatal("parallel requests stalled")
		}
	}
	if !seen[first] || !seen[second] {
		t.Fatal("wrong session scope")
	}
	once.Do(func() { close(release) })
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-f.ctx.Done():
			t.Fatal("parallel responses stalled")
		}
	}
	for _, id := range []protocol.SessionID{first, second} {
		other := first
		if id == first {
			other = second
		}
		messages, _ := json.Marshal(f.host.sessions[id].runtime.Messages())
		if !strings.Contains(string(messages), string(id)) || strings.Contains(string(messages), string(other)) {
			t.Fatal("question responses crossed sessions")
		}
	}
}
