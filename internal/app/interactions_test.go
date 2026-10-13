package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

func interactionModel() *model {
	m := fixedComposerModel(80, 24)
	m.services.Session = &session.Active{}
	m.services.Task = task.NewManager()
	return m
}

func interactionQuestion(m *model, ctx context.Context, id string) *conv.InteractionRequest {
	return &conv.InteractionRequest{
		Context: ctx, Owner: m.conv.AgentToUI,
		Source: tool.AgentInteractionSource{AgentID: id, AgentName: "worker", TaskID: id},
		Question: &conv.QuestionRequestMsg{
			Request: &tool.QuestionRequest{ID: id, Questions: []tool.Question{{Header: "Choice", Question: "Pick a value", Options: []tool.QuestionOption{{Label: "first"}}}}},
			Reply:   make(chan *tool.QuestionResponse, 1),
		},
	}
}

func interactionApproval(m *model, ctx context.Context, id string) *conv.InteractionRequest {
	return &conv.InteractionRequest{
		Context: ctx, Owner: m.conv.AgentToUI,
		Source:         tool.AgentInteractionSource{AgentID: id, AgentName: "worker", TaskID: id, PermissionMode: "default", WorkingDirectory: "/worker/cwd", ToolCallID: "worker-call"},
		Permission:     &agent.PermGateRequest{Context: ctx, ToolName: "Write", RequestID: id, Response: make(chan agent.PermGateResponse, 1)},
		PermissionView: &perm.PermissionRequest{ID: id, ToolName: "Write", CallerAgent: "worker", OnceOnly: true},
	}
}

func TestInteractionFIFOAcrossQuestionApprovalAndSecret(t *testing.T) {
	m := interactionModel()
	question := interactionQuestion(m, context.Background(), "question")
	approval := interactionApproval(m, context.Background(), "approval")
	secret := &conv.InteractionRequest{ID: 42, Owner: m.conv.AgentToUI, Context: context.Background(), Secret: &conv.SecretPromptRequestMsg{Prompt: "password", Reply: make(chan conv.SecretPromptResponse, 1)}}
	m.enqueueInteraction(question)
	m.conv.Modal.Question.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	m.conv.Modal.Question.HandleKeypress(tea.KeyPressMsg{Code: 'd', Text: "draft"})
	before := m.conv.Modal.Question.Render()
	m.enqueueInteraction(approval)
	m.enqueueInteraction(secret)
	if m.interactions.active != question || m.conv.Modal.Question.Render() != before || !strings.Contains(before, "draft") {
		t.Fatal("another interaction replaced the question or its partial input")
	}
	m.handleQuestionResponse(conv.QuestionResponseMsg{Request: question.Question.Request, Response: &tool.QuestionResponse{RequestID: "question", Answers: map[int][]string{0: {"first"}}}})
	if response := <-question.Question.Reply; response.RequestID != "question" {
		t.Fatal("question response misrouted")
	}
	if m.interactions.active != approval || !m.userInput.Approval.IsActive() || m.conv.Modal.Question.IsActive() {
		t.Fatal("approval did not exclusively own the next slot")
	}
	if options := input.BuildApprovalOptions(m.interactions.permission); len(options) != 2 {
		t.Fatalf("worker was offered broad grants: %v", options)
	}
	// A background approval works with an inactive main agent. Broad-grant flags
	// on a stale or forged response must never broaden the worker's one-shot grant.
	m.handlePermGateDecision(permissionDecision{Approved: true, AllowAll: true, Persist: true, Request: m.interactions.permission})
	if response := <-approval.Permission.Response; !response.Allow {
		t.Fatal("approval did not reach the suspended worker")
	}
	if m.interactions.active != secret || !m.userInput.Secret.IsActive() || m.userInput.Approval.IsActive() {
		t.Fatal("secret did not own the next slot")
	}
	m.handleSecretPromptResponse(input.SecretPromptResponseMsg{RequestID: 41, Value: "stale"})
	if len(secret.Secret.Reply) != 0 {
		t.Fatal("stale secret response reached another request")
	}
	m.handleSecretPromptResponse(input.SecretPromptResponseMsg{RequestID: 42, Value: "answer"})
	if response := <-secret.Secret.Reply; response.Value != "answer" {
		t.Fatal("secret response misrouted")
	}
	if m.interactions.active != nil || len(m.interactions.queue) != 0 {
		t.Fatal("completed interactions remain queued")
	}
}

func TestInteractionCancellationSkipsQueuedAndProtectsSuccessor(t *testing.T) {
	m := interactionModel()
	ctx, cancel := context.WithCancel(context.Background())
	first := interactionQuestion(m, ctx, "foreground")
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queued := interactionApproval(m, queuedCtx, "cancelled-queued")
	background := interactionQuestion(m, context.Background(), "background")
	m.enqueueInteraction(first)
	m.enqueueInteraction(queued)
	m.enqueueInteraction(background)
	cancelQueued()
	cancel()
	m.Update(conv.AgentToUITickMsg{})
	if m.interactions.active != background || len(m.interactions.queue) != 0 {
		t.Fatal("foreground cancellation discarded a live background request")
	}
	if response := <-first.Question.Reply; !response.Cancelled {
		t.Fatal("displayed question was not canceled")
	}
	if response := <-queued.Permission.Response; response.Allow {
		t.Fatal("queued cancellation granted permission")
	}
	m.handleQuestionResponse(conv.QuestionResponseMsg{Request: first.Question.Request, Response: &tool.QuestionResponse{RequestID: "foreground"}})
	if len(background.Question.Reply) != 0 || m.interactions.active != background {
		t.Fatal("stale answer consumed the background question")
	}
	m.closeInteractions()
	if response := <-background.Question.Reply; !response.Cancelled {
		t.Fatal("session close did not resolve displayed background question")
	}
}

func TestInteractionResetRejectsLateDeliveryAndResponses(t *testing.T) {
	m := interactionModel()
	old := interactionApproval(m, context.Background(), "old")
	late := interactionQuestion(m, context.Background(), "late")
	m.enqueueInteraction(old)
	oldView := m.interactions.permission
	m.resetInteractions()
	m.enqueueInteraction(late)
	if response := <-late.Question.Reply; !response.Cancelled {
		t.Fatal("old-session poll delivery survived reset")
	}
	current := interactionApproval(m, context.Background(), "current")
	m.enqueueInteraction(current)
	m.handlePermGateDecision(permissionDecision{Approved: true, Request: oldView})
	if len(current.Permission.Response) != 0 || m.interactions.active != current {
		t.Fatal("old approval answered a new session's request")
	}
	m.handlePermGateDecision(permissionDecision{Request: m.interactions.permission})
	if response := <-current.Permission.Response; response.Allow {
		t.Fatal("reject became approval")
	}
}

func TestInteractionDefersAllKindsBehindHistoryAndOverlays(t *testing.T) {
	m := interactionModel()
	m.flush.queueScrollbackPrint("settled", 0)
	question := interactionQuestion(m, context.Background(), "question")
	approval := interactionApproval(m, context.Background(), "approval")
	m.enqueueInteraction(question)
	m.enqueueInteraction(approval)
	if m.interactions.active != question || m.interactions.shown || m.conv.Modal.Question.IsActive() {
		t.Fatal("deferred question lost its slot or entered history")
	}
	m.flush.pendingPrints = nil
	m.advanceInteractions()
	if !m.conv.Modal.Question.IsActive() || m.interactions.active != question {
		t.Fatal("history completion did not show the original question")
	}
	m.closeInteractions()
	m.resetInteractions()
	m.userInput.Settings.Enter(m.env.Width, m.env.Height)
	approval = interactionApproval(m, context.Background(), "after-overlay")
	m.enqueueInteraction(approval)
	if m.userInput.Approval.IsActive() {
		t.Fatal("worker approval replaced an existing overlay")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.interactions.active != approval || !m.userInput.Approval.IsActive() {
		t.Fatal("closing overlay did not advance the interaction queue")
	}
}
