package agent

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/runtime/mail"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// SendMessageTool routes a message to another agent through the broker:
// main → an existing subagent (by task id), or a subagent → "main". The message
// lands in the recipient's inbox and is read at its next step (a subagent) or
// turn boundary (main). Acceptance is not an acknowledgment or completion;
// idle workers continue in their original session when the parent is authorized.
type SendMessageTool struct{ executor tool.AgentExecutor }

func (t *SendMessageTool) SetExecutor(executor tool.AgentExecutor) { t.executor = executor }

func NewSendMessageTool() *SendMessageTool { return &SendMessageTool{} }

func (t *SendMessageTool) Name() string { return tool.ToolSendMessage }
func (t *SendMessageTool) Description() string {
	return "Send a message to another agent via the broker: main → an existing subagent (task id), or a subagent → \"main\""
}
func (t *SendMessageTool) Icon() string             { return tool.IconAgent }
func (t *SendMessageTool) RequiresPermission() bool { return true }

func (t *SendMessageTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	message, err := tool.RequireString(params, "message")
	if err != nil {
		return nil, err
	}
	subject := strings.TrimSpace(tool.GetString(params, "subject"))
	to := strings.TrimSpace(tool.GetString(params, "to"))
	if to == "" {
		return nil, fmt.Errorf("to is required (a subagent task id, \"main\", or a group member's name)")
	}
	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		Description: fmt.Sprintf("Message %s: %s", recipientLabel(task.FromContext(ctx), to), strings.TrimSpace(subject+"\n\n"+message)),
	}, nil
}

func (t *SendMessageTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.execute(ctx, params)
}

func (t *SendMessageTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.execute(ctx, params)
}

func (t *SendMessageTool) execute(ctx context.Context, params map[string]any) toolresult.ToolResult {
	start := time.Now()
	tasks := task.FromContext(ctx)

	message := tool.GetString(params, "message")
	if message == "" {
		return toolresult.NewErrorResult(t.Name(), "message is required")
	}
	subject := strings.TrimSpace(tool.GetString(params, "subject"))
	to := strings.TrimSpace(tool.GetString(params, "to"))
	if to == "" {
		return toolresult.NewErrorResult(t.Name(), "to is required (a subagent task id, \"main\", or a group member's name)")
	}

	// A member of this session's group lives in another process: its inbox is
	// a directory, not a broker route.
	if _, ok := groupMember(tasks, to); ok {
		if tool.AgentIDFromContext(ctx) != "" {
			return toolresult.NewErrorResult(t.Name(), "only the main conversation can message group members; report to it instead")
		}
		member, err := group.Send(to, subject, message)
		if err != nil {
			return toolresult.NewErrorResult(t.Name(), err.Error())
		}
		return toolresult.ToolResult{
			Success: true,
			Output:  sentTo(member),
			Metadata: toolresult.ResultMetadata{
				Title:    t.Name(),
				Icon:     t.Icon(),
				Subtitle: "→ @" + to,
				Duration: time.Since(start),
			},
		}
	}

	from := tool.AgentIDFromContext(ctx)
	if from == "" {
		from = mail.Main
	}
	if from == to {
		return toolresult.NewErrorResult(t.Name(), "cannot send a message to yourself")
	}

	// A stale broker route must never make an inactive execution or a non-agent
	// task messageable. Idle sessions must pass the continuation gate.
	var observedWorker *task.AgentTask
	var observedAttempt int
	if bgTask, found := tasks.Get(to); found {
		info := bgTask.GetStatus()
		if sessionID := task.SessionIDFromContext(ctx); sessionID != "" && info.ParentSessionID != "" && sessionID != info.ParentSessionID {
			return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("task %q belongs to a different parent session; use a running agent task ID from this session", to))
		}
		if bgTask.GetType() != task.TaskTypeAgent {
			return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("task %q is a %s task, not an agent with a message inbox; inspect its output file and continue with the shell tools", to, bgTask.GetType()))
		}
		observedWorker, _ = bgTask.(*task.AgentTask)
		observedAttempt = info.Attempt
		if info.Status != task.StatusRunning {
			if observedWorker != nil && (info.Status == task.StatusCompleted || info.Status == task.StatusFailed) {
				return t.handoffClosingWorker(ctx, observedWorker, observedAttempt, subject, message)
			}
			return t.continueWorker(ctx, info, subject, message)
		}
	}

	display := brokerSubject(tasks, from, subject)
	sourceSession, sourceSeq := tool.MessageSourceFromContext(ctx)
	sendErr := mail.SendErr(mail.Message{
		SourceSession: sourceSession,
		SourceSeq:     sourceSeq,
		SessionID:     task.SessionIDFromContext(ctx),
		From:          from,
		To:            to,
		Subject:       display,
		Content:       wrapAgentMessage(from, display, message),
	})
	if sendErr != nil {
		if errors.Is(sendErr, mail.ErrInboxClosing) && observedWorker != nil {
			return t.handoffClosingWorker(ctx, observedWorker, observedAttempt, subject, message)
		}
		if errors.Is(sendErr, mail.ErrNoRecipient) && observedWorker != nil {
			current, found := tasks.Get(to)
			info := observedWorker.GetStatus()
			if found && current == observedWorker && info.Attempt == observedAttempt &&
				(info.Status == task.StatusCompleted || info.Status == task.StatusFailed) &&
				tool.AgentIDFromContext(ctx) == "" && task.SessionIDFromContext(ctx) != "" && task.SessionIDFromContext(ctx) == info.ParentSessionID {
				return t.handoffClosingWorker(ctx, observedWorker, observedAttempt, subject, message)
			}
		}
		return toolresult.NewErrorResult(t.Name(), undeliverableReason(tasks, to, sendErr))
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  toolresult.SendMessageQueuedPrefix + recipientLabel(tasks, to) + "; the recipient reads queued messages at its next step or turn boundary. This is not an acknowledgment or completion. Continue with your task.",
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: "→ " + recipientLabel(tasks, to),
			Duration: time.Since(start),
		},
	}
}

// brokerSubject is the line the receiving side shows for a message.
func brokerSubject(tasks *task.RunSet, from, subject string) string {
	if subject == "" {
		return "Message from " + senderLabel(tasks, from)
	}
	return fmt.Sprintf("From %s: %s", senderLabel(tasks, from), subject)
}

// sentTo says when a member will read the message, so the sender knows
// whether to wait for a reply.
func sentTo(m group.Member) string {
	return groupQueueStatus(m) + " Queued mail is not an acknowledgment or completion."
}

func groupQueueStatus(m group.Member) string {
	switch {
	case !m.Online():
		return fmt.Sprintf("Queued · offline; @%s sees it when its session resumes.", m.Name)
	case m.Hold:
		return fmt.Sprintf("Queued · waits for its user; @%s holds messages until its user next types — don't wait for a reply.", m.Name)
	case m.State == group.Approval:
		return fmt.Sprintf("Queued · waits on its user's approval; @%s is stopped on an approval, so a reply may take a while.", m.Name)
	case m.State == group.Working:
		return fmt.Sprintf("Queued · reads it between steps; @%s is busy, so it reads it between its current steps.", m.Name)
	}
	return fmt.Sprintf("Queued · awaiting next turn; @%s is idle and can handle it at its next turn boundary.", m.Name)
}

// recipientLabel renders a target id for tool output.
func recipientLabel(tasks *task.RunSet, id string) string {
	if id == mail.Main {
		return "the main conversation"
	}
	if g, ok := groupMember(tasks, id); ok {
		return "@" + id + " in group " + g
	}
	return senderLabel(tasks, id)
}

// groupMember reports whether to names a member of this session's group
// rather than "main" or a task, and which group.
func groupMember(tasks *task.RunSet, to string) (string, bool) {
	g, _ := group.Current()
	if g == "" || to == mail.Main {
		return "", false
	}
	_, isTask := tasks.Get(to)
	return g, !isTask
}

// senderLabel names an agent id for a human-facing notice.
func senderLabel(tasks *task.RunSet, id string) string {
	if id == mail.Main {
		return "the main conversation"
	}
	if bgTask, found := tasks.Get(id); found {
		if info := bgTask.GetStatus(); info.AgentName != "" {
			return fmt.Sprintf("%s (task %s)", info.AgentName, id)
		}
	}
	return "task " + id
}

// undeliverableReason reports the recipient's stated rejection and observed
// task lifecycle, never an assumed transport cause or blind retry advice.
func undeliverableReason(tasks *task.RunSet, to string, sendErr error) string {
	if to == mail.Main {
		return "the main conversation is not currently reachable"
	}
	bgTask, found := tasks.Get(to)
	if !found {
		return fmt.Sprintf("no worker task is known at %q; check the exact task ID returned by Agent and inspect existing results before deciding how to continue", to)
	}
	info := bgTask.GetStatus()
	if info.Status == task.StatusRunning && errors.Is(sendErr, mail.ErrInboxFull) {
		return fmt.Sprintf("worker %q is running but already has the maximum number of unread messages; this message was not queued and will not be retried automatically. You still own this instruction: do not retry or poll in a loop; once the worker drains its queue, replies, or completes, deliver it only if it is still needed", to)
	}
	if info.Status == task.StatusRunning {
		return fmt.Sprintf("worker %q is still marked running, but message delivery failed: %v. This message was not queued; no continuation was started", to, sendErr)
	}
	if info.Status == task.StatusStopped || info.Status == task.StatusKilled {
		return terminalMessageReason(to, info.Status)
	}
	return fmt.Sprintf("worker %q (status %s) did not accept the message: %v. This message was not queued; no continuation was started", to, info.Status, sendErr)
}

func terminalMessageReason(to string, status task.TaskStatus) string {
	if status == task.StatusStopped || status == task.StatusKilled {
		return fmt.Sprintf("worker %q was deliberately stopped (status %s); messages cannot restart it and it cannot be resumed automatically. Ask the user before starting new work", to, status)
	}
	return fmt.Sprintf("worker %q has no active execution (status %s); send follow-up instructions from its owning main conversation to continue the same task and session", to, status)
}

// wrapAgentMessage tags peer mail so the recipient can tell it from real user
// input, mirroring the <task-notification> convention. The body keeps its
// newlines (tool.EscapeXMLText only neutralizes &, <, >), so multi-line
// messages read naturally in the recipient's context.
func wrapAgentMessage(from, subject, text string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<agent-message from=\"%s\" subject=\"%s\">\n", html.EscapeString(from), html.EscapeString(subject))
	b.WriteString(tool.EscapeXMLText(text))
	b.WriteString("\n</agent-message>")
	return b.String()
}

func init() {
	tool.Register(NewSendMessageTool())
}

// Keep child transcripts and output out of the parent's approval context.
type continuationApproval struct {
	TaskID  string `json:"task_id"`
	Attempt int    `json:"attempt"`
}

// A sealed inbox, or a route removed after terminal publication, can hand off
// the same execution. Missing routes never wait for a still-running worker.
func (t *SendMessageTool) handoffClosingWorker(ctx context.Context, worker *task.AgentTask, attempt int, subject, message string) toolresult.ToolResult {
	fail := func(reason string) toolresult.ToolResult { return toolresult.NewErrorResult(t.Name(), reason) }
	info := worker.GetStatus()
	owner := task.SessionIDFromContext(ctx)
	if tool.AgentIDFromContext(ctx) != "" || owner == "" || owner != info.ParentSessionID {
		return fail("only the owning main conversation can continue a closing agent; this message was not queued")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := worker.WaitForAttemptCompletionContext(waitCtx, attempt); err != nil {
		return fail(fmt.Sprintf("worker %q is closing; this message was not queued and no continuation was started: %v", info.ID, err))
	}
	current, found := task.FromContext(ctx).Get(info.ID)
	info = worker.GetStatus()
	if !found || current != worker || info.Attempt != attempt || info.ParentSessionID != owner {
		return fail("selected agent execution changed during closing; this message was not queued and no continuation was started")
	}
	return t.continueWorker(ctx, info, subject, message)
}

func (t *SendMessageTool) continueWorker(ctx context.Context, info task.TaskInfo, subject, message string) toolresult.ToolResult {
	fail := func(reason string) toolresult.ToolResult { return toolresult.NewErrorResult(t.Name(), reason) }
	if tool.AgentIDFromContext(ctx) != "" || task.SessionIDFromContext(ctx) == "" || task.SessionIDFromContext(ctx) != info.ParentSessionID {
		return fail("only the owning main conversation can continue an idle agent")
	}
	if info.Status == task.StatusStopped || info.Status == task.StatusKilled {
		return fail(terminalMessageReason(info.ID, info.Status))
	}
	if !task.RecoveryBudgetAvailable(info.Attempts) {
		return fail("agent exhausted its corrective recovery budget; inspect existing results and report the blocker")
	}
	if t.executor == nil {
		return fail("agent executor unavailable; no new conversation was started")
	}
	// The typed task snapshot cannot be supplied by model JSON. The Agent gate
	// uses it to preview the original policy and pins approval to this attempt.
	input := map[string]any{"name": info.AgentName, "prompt": message, "description": info.Description, "run_in_background": true, "_continuation": continuationApproval{TaskID: info.ID, Attempt: info.Attempt}}
	if allowed, reason := tool.CheckPermission(ctx, tool.ToolAgent, input); !allowed {
		return fail(reason)
	}
	if err := ctx.Err(); err != nil {
		return fail(err.Error())
	}
	sourceSession, sourceSeq := tool.MessageSourceFromContext(ctx)
	next, err := t.executor.RunBackground(tool.AgentExecRequest{
		SourceSession: sourceSession, SourceSeq: sourceSeq,
		ResumeTaskID: info.ID, ResumeAttempt: info.Attempt, Prompt: wrapAgentMessage(mail.Main, subject, message),
		ParentToolCallID: core.ToolCallIDFromContext(ctx), Background: true,
	})
	if err != nil {
		return fail(err.Error())
	}
	return toolresult.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Message accepted; agent %s started its next execution in the original session. Completion will be notified separately.", info.ID),
		Details: toolresult.AgentDetails{TaskID: info.ID, Identity: next.Identity},
		HookResponse: map[string]any{
			"backgroundTask": map[string]any{
				"taskId": info.ID, "agentName": info.AgentName,
				"description": info.Description, "outputFile": next.OutputFile, "toolName": t.Name(),
			},
		},
		Metadata: toolresult.ResultMetadata{Title: t.Name(), Icon: t.Icon()},
	}
}
