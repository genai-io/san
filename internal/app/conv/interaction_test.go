package conv

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

func receiveInteraction(t *testing.T, bridge *AgentToUI) *InteractionRequest {
	t.Helper()
	select {
	case request := <-bridge.requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("interaction was not enqueued")
		return nil
	}
}

func TestInteractionsUseOriginalReplyEndpoints(t *testing.T) {
	bridge := NewAgentToUI(4)
	defer bridge.Close()
	questions := make(chan *tool.QuestionResponse, 2)
	for _, id := range []string{"first", "second"} {
		go func() {
			response, err := bridge.Ask(context.Background(), tool.AgentInteractionSource{AgentID: id}, &tool.QuestionRequest{ID: id})
			if err != nil {
				t.Error(err)
			}
			questions <- response
		}()
	}
	first, second := receiveInteraction(t, bridge), receiveInteraction(t, bridge)
	if first.Question.Request == second.Question.Request || first.ID == second.ID {
		t.Fatal("concurrent requests share an identity")
	}
	second.Question.Reply <- &tool.QuestionResponse{RequestID: second.Question.Request.ID}
	select {
	case response := <-questions:
		if response.RequestID != second.Question.Request.ID {
			t.Fatal("answer reached a different worker")
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not receive its answer")
	}
	first.Question.Reply <- &tool.QuestionResponse{RequestID: first.Question.Request.ID}
	select {
	case response := <-questions:
		if response.RequestID != first.Question.Request.ID {
			t.Fatal("first worker lost its answer")
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not receive its answer")
	}
}

func TestInteractionCloseUnblocksEveryStage(t *testing.T) {
	for _, stage := range []string{"enqueue", "queued", "delivered"} {
		t.Run(stage, func(t *testing.T) {
			bridge := NewAgentToUI(1)
			if stage == "enqueue" {
				bridge.requests <- &InteractionRequest{}
			}
			done := make(chan error, 1)
			go func() {
				_, err := bridge.Ask(context.Background(), tool.AgentInteractionSource{}, &tool.QuestionRequest{ID: "pending"})
				done <- err
			}()
			var req *InteractionRequest
			if stage == "delivered" {
				req = receiveInteraction(t, bridge)
			}
			if stage == "queued" {
				req = receiveInteraction(t, bridge)
				bridge.requests <- req
			}
			bridge.Close()
			bridge.Close()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("closed UI answered a question")
				}
			case <-time.After(time.Second):
				t.Fatal("closed UI left a caller waiting")
			}
			if req != nil {
				select {
				case <-req.Context.Done():
				case <-time.After(time.Second):
					t.Fatal("delivered request survived closure")
				}
			}
			if _, err := bridge.Ask(context.Background(), tool.AgentInteractionSource{}, &tool.QuestionRequest{}); !errors.Is(err, ErrInteractionUnavailable) {
				t.Fatalf("late request error = %v", err)
			}
			if bridge.Check() != nil {
				t.Fatal("closed transport kept polling")
			}
		})
	}
}

func TestInteractionCallerCancellationAndFailClosedApproval(t *testing.T) {
	bridge := NewAgentToUI(1)
	defer bridge.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		approved, _ := bridge.Approve(ctx, tool.AgentInteractionSource{AgentID: "worker"}, &perm.PermissionRequest{ID: "approval", ToolName: "Write"}, nil)
		done <- approved
	}()
	req := receiveInteraction(t, bridge)
	cancel()
	select {
	case approved := <-done:
		if approved {
			t.Fatal("cancellation granted approval")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation left the gate waiting")
	}
	if req.Context.Err() == nil {
		t.Fatal("UI cannot observe request cancellation")
	}
	closed := NewAgentToUI(1)
	closed.Close()
	if approved, _ := closed.Approve(context.Background(), tool.AgentInteractionSource{}, &perm.PermissionRequest{}, nil); approved {
		t.Fatal("closed UI granted approval")
	}
}
