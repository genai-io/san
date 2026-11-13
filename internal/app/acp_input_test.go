package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func acpFormInit() protocol.InitializeRequest {
	return protocol.InitializeRequest{ProtocolVersion: 1, ClientCapabilities: protocol.Some(protocol.ClientCapabilities{Elicitation: protocol.Some(protocol.ElicitationCapabilities{Form: protocol.Some(protocol.ElicitationFormCapabilities{})})})}
}
func acpAccepted(content string) protocol.CreateElicitationResponse {
	return protocol.CreateElicitationResponse{Value: protocol.CreateElicitationResponseAcceptVariant{Action: "accept", Content: protocol.Some(json.RawMessage(content))}}
}

func TestACPQuestions(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
		action              string
	}{
		{name: "choice", content: `{"q0":"1"}`, want: "Colour → Blue"},
		{name: "other", content: `{"q0":"other","q0_text":"Green"}`, want: "Colour → Green"},
		{name: "unknown option", content: `{"q0":"99"}`, want: "unknown or empty ACP answer"},
		{name: "unknown field", content: `{"q0":"0","injected":"bad"}`, want: "unknown ACP form field"},
		{name: "missing", content: `{}`, want: "missing ACP form field"},
		{name: "null", content: `{"q0":null}`, want: "Internal error"},
		{name: "wrong type", content: `{"q0":true}`, want: "invalid ACP selection"},
		{name: "empty other", content: `{"q0":"other"}`, want: "unknown or empty ACP answer"},
		{name: "extra text", content: `{"q0":"0","q0_text":"bad"}`, want: "free text requires Other"},
		{name: "decline", action: "decline", want: "cancelled the question"},
		{name: "cancel", action: "cancel", want: "cancelled the question"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "question", Name: "AskUserQuestion", Input: `{"question":"Colour","options":["Red","Blue"]}`}), aitest.Says("done"))
			var calls atomic.Int32
			f := newACPFixtureOptions(t, driver, nil, acpWriteTimeout, acpFormInit(), func(_ context.Context, req protocol.CreateElicitationRequest) (protocol.CreateElicitationResponse, error) {
				calls.Add(1)
				v, ok := req.Value.(protocol.CreateElicitationRequestFormVariant)
				if !ok || !v.SessionID.Present || v.RequestID.Present {
					t.Error("invalid form scope")
				}
				wire, _ := json.Marshal(req)
				if !strings.Contains(string(wire), `"oneOf"`) || !strings.Contains(string(wire), `"q0_text"`) {
					t.Error("missing options/free text")
				}
				switch tc.action {
				case "cancel":
					return protocol.CreateElicitationResponse{Value: protocol.CreateElicitationResponseCancelVariant{Action: "cancel"}}, nil
				case "decline":
					return protocol.CreateElicitationResponse{Value: protocol.CreateElicitationResponseDeclineVariant{Action: "decline"}}, nil
				default:
					return acpAccepted(tc.content), nil
				}
			})
			id := f.session(t, t.TempDir())
			if _, err := f.prompt(id, "ask"); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls=%d", calls.Load())
			}
			sent, _ := json.Marshal(driver.Sent())
			if !strings.Contains(string(sent), tc.want) {
				t.Fatalf("missing result %q", tc.want)
			}
			if f.host.sessions[id].input.current() != nil {
				t.Fatal("turn retained after prompt")
			}
		})
	}
}

func TestACPQuestionsWithoutCapability(t *testing.T) {
	driver := aitest.New(aitest.Says("done"))
	f := newACPFixture(t, driver, nil)
	id := f.session(t, t.TempDir())
	s := f.host.sessions[id]
	if !s.runtimeBuildParams.DisabledTools["AskUserQuestion"] || s.runtimeBuildParams.AskUser != nil || s.runtimeBuildParams.BashPromptResponder != nil {
		t.Fatal("unnegotiated interaction exposed")
	}
	if _, err := f.prompt(id, "hello"); err != nil {
		t.Fatal(err)
	}
	for _, schema := range driver.Sent()[0].Tools {
		if schema.Name == "AskUserQuestion" {
			t.Fatal("question offered without capability")
		}
	}
}

func TestACPInputCancellation(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{true: "disconnect", false: "cancel"}[disconnect], func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "question", Name: "AskUserQuestion", Input: `{"options":["A","B"]}`}))
			f := newACPFixtureOptions(t, driver, nil, acpWriteTimeout, acpFormInit(), func(ctx context.Context, _ protocol.CreateElicitationRequest) (protocol.CreateElicitationResponse, error) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
				return acpAccepted(`{"q0":"0"}`), nil
			})
			id := f.session(t, t.TempDir())
			done := make(chan error, 1)
			go func() { _, err := f.prompt(id, "ask"); done <- err }()
			select {
			case <-entered:
			case <-f.ctx.Done():
				t.Fatal("no elicitation")
			}
			if disconnect {
				_ = f.client.Close()
			} else if err := f.client.CancelPrompt(f.ctx, protocol.CancelNotification{SessionID: id}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !disconnect && err != nil {
					t.Fatal(err)
				}
			case <-f.ctx.Done():
				t.Fatal("prompt did not cancel")
			}
			if driver.Calls() != 1 {
				t.Fatalf("cancelled elicitation started successor inference: calls=%d", driver.Calls())
			}
		})
	}
}

func TestACPSecretUnsupported(t *testing.T) {
	var input acpInput
	if value, ok := input.RequestSecret(context.Background(), "password:"); ok || value != "" {
		t.Fatal("secret did not fail closed")
	}
	if value, ok := input.RequestAnswer(context.Background(), "command", "input:"); ok || value != "" {
		t.Fatal("unowned prompt did not fail closed")
	}
}
