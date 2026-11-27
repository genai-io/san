package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

func userPromptModel() *model {
	m := fixedComposerModel(80, 24)
	m.services.Session = nil
	m.services.Task = task.NewManager()
	m.env.SessionPermissions = setting.NewSessionPermissions()
	return m
}

func userPromptQuestion(m *model, ctx context.Context, id string) *chat.UserPromptRequest {
	return &chat.UserPromptRequest{
		Context: ctx, Owner: m.conv.AgentToUI,
		Source: tool.AgentInteractionSource{AgentID: id, AgentName: "worker", TaskID: id},
		Question: &chat.QuestionRequestMsg{
			Request: &tool.QuestionRequest{ID: id, Questions: []tool.Question{{Header: "Choice", Question: "Pick a value", Options: []tool.QuestionOption{{Label: "first"}}}}},
			Reply:   make(chan *tool.QuestionResponse, 1),
		},
	}
}

func userPromptApproval(m *model, ctx context.Context, id string) *chat.UserPromptRequest {
	return &chat.UserPromptRequest{
		Context: ctx, Owner: m.conv.AgentToUI,
		Source:         tool.AgentInteractionSource{AgentID: id, AgentName: "worker", TaskID: id, PermissionMode: "default", WorkingDirectory: "/worker/cwd", ToolCallID: "worker-call"},
		Permission:     &runtime.PermGateRequest{Context: ctx, ToolName: "Write", RequestID: id, Response: make(chan runtime.PermGateResponse, 1)},
		PermissionView: &perm.PermissionRequest{ID: id, ToolName: "Write", CallerAgent: "worker", OnceOnly: true},
	}
}

func TestInteractionApprovalRecoversOnlyExactUnambiguousDisplayIdentity(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		m := userPromptModel()
		request := userPromptApproval(m, context.Background(), "request")
		request.Source.AgentID = ""
		request.Permission.ToolName = "Agent"
		request.Permission.Input = map[string]any{"prompt": "Review", "_resolvedAgentConfig": "runtime-only"}
		calls := []core.ToolCall{{ID: "exact", Name: "Agent", Input: `{"prompt":"Review"}`}}
		if ambiguous {
			calls = append(calls, core.ToolCall{ID: "identical-peer", Name: "Agent", Input: `{"prompt":"Review"}`})
		}
		m.enqueueUserPrompt(request)
		m.conv.Tool.Track(calls)
		m.advanceUserPrompts()
		want := "exact"
		if ambiguous {
			want = ""
		}
		if m.conv.Tool.AwaitingApprovalID != want {
			t.Fatalf("ambiguous=%v: display ownership=%q, want %q", ambiguous, m.conv.Tool.AwaitingApprovalID, want)
		}
		if request.Permission.ToolCallID != "" || len(request.Permission.Input) != 2 {
			t.Fatal("display fallback mutated request identity or execution arguments")
		}
	}
}

func TestInteractionApprovalSurvivesLateToolTracking(t *testing.T) {
	for _, worker := range []bool{false, true} {
		m := userPromptModel()
		request := userPromptApproval(m, context.Background(), "request")
		request.Permission.ToolCallID = "actual-call"
		if worker {
			request.Source.ParentToolCallID = "parent-call"
		} else {
			request.Source.AgentID = ""
		}
		want := request.Permission.ToolCallID
		if worker {
			want = request.Source.ParentToolCallID
		}
		m.enqueueUserPrompt(request)
		m.conv.Tool.Track([]core.ToolCall{{ID: want, Name: "Agent"}})
		m.advanceUserPrompts()
		if m.conv.Tool.AwaitingApprovalID != want || m.userPrompts.active != request || !m.userInput.Approval.IsActive() {
			t.Fatalf("late tracking lost exact approval ownership: worker=%v call=%q", worker, m.conv.Tool.AwaitingApprovalID)
		}
	}
}

func TestInteractionFIFOAcrossQuestionApprovalAndSecret(t *testing.T) {
	m := userPromptModel()
	question := userPromptQuestion(m, context.Background(), "question")
	approval := userPromptApproval(m, context.Background(), "approval")
	secret := &chat.UserPromptRequest{ID: 42, Owner: m.conv.AgentToUI, Context: context.Background(), Secret: &chat.SecretPromptRequestMsg{Prompt: "password", Reply: make(chan chat.SecretPromptResponse, 1)}}
	m.enqueueUserPrompt(question)
	m.conv.Modal.Question.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	m.conv.Modal.Question.HandleKeypress(tea.KeyPressMsg{Code: 'd', Text: "draft"})
	before := m.conv.Modal.Question.Render()
	m.enqueueUserPrompt(approval)
	m.enqueueUserPrompt(secret)
	if m.userPrompts.active != question || m.conv.Modal.Question.Render() != before || !strings.Contains(before, "draft") {
		t.Fatal("another interaction replaced the question or its partial input")
	}
	m.handleQuestionResponse(chat.QuestionResponseMsg{Request: question.Question.Request, Response: &tool.QuestionResponse{RequestID: "question", Answers: map[int][]string{0: {"first"}}}})
	if response := <-question.Question.Reply; response.RequestID != "question" {
		t.Fatal("question response misrouted")
	}
	if m.userPrompts.active != approval || !m.userInput.Approval.IsActive() || m.conv.Modal.Question.IsActive() {
		t.Fatal("approval did not exclusively own the next slot")
	}
	if options := input.BuildApprovalOptions(m.userPrompts.permission); len(options) != 2 {
		t.Fatalf("worker was offered broad grants: %v", options)
	}
	// A background approval works with an inactive main agent. Broad-grant flags
	// on a stale or forged response must never broaden the worker's one-shot grant.
	m.handlePermGateDecision(permissionDecision{Approved: true, AllowAll: true, Persist: true, Request: m.userPrompts.permission})
	if response := <-approval.Permission.Response; !response.Allow {
		t.Fatal("approval did not reach the suspended worker")
	}
	if m.userPrompts.active != secret || !m.userInput.Secret.IsActive() || m.userInput.Approval.IsActive() {
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
	if m.userPrompts.active != nil || len(m.userPrompts.queue) != 0 {
		t.Fatal("completed interactions remain queued")
	}
}

func TestInteractionCancellationSkipsQueuedAndProtectsSuccessor(t *testing.T) {
	m := userPromptModel()
	ctx, cancel := context.WithCancel(context.Background())
	first := userPromptQuestion(m, ctx, "foreground")
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queued := userPromptApproval(m, queuedCtx, "cancelled-queued")
	background := userPromptQuestion(m, context.Background(), "background")
	m.enqueueUserPrompt(first)
	m.enqueueUserPrompt(queued)
	m.enqueueUserPrompt(background)
	cancelQueued()
	cancel()
	m.Update(chat.AgentToUITickMsg{})
	if m.userPrompts.active != background || len(m.userPrompts.queue) != 0 {
		t.Fatal("foreground cancellation discarded a live background request")
	}
	if response := <-first.Question.Reply; !response.Cancelled {
		t.Fatal("displayed question was not canceled")
	}
	if response := <-queued.Permission.Response; response.Allow {
		t.Fatal("queued cancellation granted permission")
	}
	m.handleQuestionResponse(chat.QuestionResponseMsg{Request: first.Question.Request, Response: &tool.QuestionResponse{RequestID: "foreground"}})
	if len(background.Question.Reply) != 0 || m.userPrompts.active != background {
		t.Fatal("stale answer consumed the background question")
	}
	m.closeUserPrompts()
	if response := <-background.Question.Reply; !response.Cancelled {
		t.Fatal("session close did not resolve displayed background question")
	}
}

func TestInteractionResetRejectsLateDeliveryAndResponses(t *testing.T) {
	m := userPromptModel()
	old := userPromptApproval(m, context.Background(), "old")
	late := userPromptQuestion(m, context.Background(), "late")
	m.enqueueUserPrompt(old)
	oldView := m.userPrompts.permission
	m.resetUserPrompts()
	m.enqueueUserPrompt(late)
	if response := <-late.Question.Reply; !response.Cancelled {
		t.Fatal("old-session poll delivery survived reset")
	}
	current := userPromptApproval(m, context.Background(), "current")
	m.enqueueUserPrompt(current)
	m.handlePermGateDecision(permissionDecision{Approved: true, Request: oldView})
	if len(current.Permission.Response) != 0 || m.userPrompts.active != current {
		t.Fatal("old approval answered a new session's request")
	}
	m.handlePermGateDecision(permissionDecision{Request: m.userPrompts.permission})
	if response := <-current.Permission.Response; response.Allow {
		t.Fatal("reject became approval")
	}
}

func TestInteractionDefersAllKindsBehindHistoryAndOverlays(t *testing.T) {
	m := userPromptModel()
	m.flush.queueScrollbackPrint("settled", 0)
	question := userPromptQuestion(m, context.Background(), "question")
	approval := userPromptApproval(m, context.Background(), "approval")
	m.enqueueUserPrompt(question)
	m.enqueueUserPrompt(approval)
	if m.userPrompts.active != question || m.userPrompts.shown || m.conv.Modal.Question.IsActive() {
		t.Fatal("deferred question lost its slot or entered history")
	}
	m.flush.pendingPrints = nil
	m.advanceUserPrompts()
	if !m.conv.Modal.Question.IsActive() || m.userPrompts.active != question {
		t.Fatal("history completion did not show the original question")
	}
	m.closeUserPrompts()
	m.resetUserPrompts()
	m.userInput.Settings.Enter(m.env.Width, m.env.Height)
	approval = userPromptApproval(m, context.Background(), "after-overlay")
	m.enqueueUserPrompt(approval)
	if m.userInput.Approval.IsActive() {
		t.Fatal("worker approval replaced an existing overlay")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.userPrompts.active != approval || !m.userInput.Approval.IsActive() {
		t.Fatal("closing overlay did not advance the interaction queue")
	}
}
