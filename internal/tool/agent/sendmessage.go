package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// SendMessageTool routes a message to another agent through the broker:
// main → a running subagent (by task id), or a subagent → "main". The message
// lands in the recipient's inbox and is read at its next step (a subagent) or
// turn boundary (main). Delivery is best-effort — a message to an agent that
// has finished, or never reads again, is dropped.
type SendMessageTool struct{}

func NewSendMessageTool() *SendMessageTool { return &SendMessageTool{} }

func (t *SendMessageTool) Name() string { return tool.ToolSendMessage }
func (t *SendMessageTool) Description() string {
	return "Send a message to another agent via the broker: main → a running subagent (task id), or a subagent → \"main\""
}
func (t *SendMessageTool) Icon() string             { return tool.IconAgent }
func (t *SendMessageTool) RequiresPermission() bool { return true }

func (t *SendMessageTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	message, err := tool.RequireString(params, "message")
	if err != nil {
		return nil, err
	}
	to := strings.TrimSpace(tool.GetString(params, "to"))
	if to == "" {
		return nil, fmt.Errorf("to is required (a subagent task id, or \"main\")")
	}
	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		Description: fmt.Sprintf("Message %s: %s", recipientLabel(to), message),
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

	message := tool.GetString(params, "message")
	if message == "" {
		return toolresult.NewErrorResult(t.Name(), "message is required")
	}
	to := strings.TrimSpace(tool.GetString(params, "to"))
	if to == "" {
		return toolresult.NewErrorResult(t.Name(), "to is required (a subagent task id, or \"main\")")
	}

	from := tool.AgentIDFromContext(ctx)
	if from == "" {
		from = broker.Main
	}
	if from == to {
		return toolresult.NewErrorResult(t.Name(), "cannot send a message to yourself")
	}

	delivered := broker.Send(broker.Message{
		From:    from,
		To:      to,
		Subject: fmt.Sprintf("Message from %s", senderLabel(from)),
		Content: wrapAgentMessage(from, message),
	})
	if !delivered {
		return toolresult.NewErrorResult(t.Name(), undeliverableReason(to))
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Message delivered to %s; it is read at the recipient's next step. Continue with your task.", recipientLabel(to)),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: "→ " + recipientLabel(to),
			Duration: time.Since(start),
		},
	}
}

// recipientLabel renders a target id for tool output.
func recipientLabel(id string) string {
	if id == broker.Main {
		return "the main conversation"
	}
	return "running " + senderLabel(id)
}

// senderLabel names an agent id for a human-facing notice.
func senderLabel(id string) string {
	if id == broker.Main {
		return "the main conversation"
	}
	if bgTask, found := task.Default().Get(id); found {
		if info := bgTask.GetStatus(); info.AgentName != "" {
			return fmt.Sprintf("%s (task %s)", info.AgentName, id)
		}
	}
	return "task " + id
}

// undeliverableReason explains why a Send to `to` was not accepted, reading the
// recipient's actual task state so the model gets an accurate next step instead
// of a blanket "finished" verdict: a just-spawned worker is still starting up
// (its broker route is registered a beat after its task id is handed out), a
// running one may have a momentarily full inbox, and only a terminal one truly
// cannot be reached.
func undeliverableReason(to string) string {
	if to == broker.Main {
		return "the main conversation is not currently reachable"
	}
	bgTask, found := task.Default().Get(to)
	if !found {
		return fmt.Sprintf("no running worker is registered at %q — it has finished or was never started; spawn a new agent to continue the work", to)
	}
	if bgTask.GetStatus().Status == task.StatusRunning {
		return fmt.Sprintf("worker %q is still starting up or its inbox is momentarily full — wait a moment and send again", to)
	}
	return fmt.Sprintf("worker %q has finished (status %s) and can no longer be messaged; spawn a new agent to continue its work", to, bgTask.GetStatus().Status)
}

// wrapAgentMessage tags peer mail so the recipient can tell it from real user
// input, mirroring the <task-notification> convention. The body keeps its
// newlines (tool.EscapeXMLText only neutralizes &, <, >), so multi-line
// messages read naturally in the recipient's context.
func wrapAgentMessage(from, text string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<agent-message from=%q>\n", from)
	b.WriteString(tool.EscapeXMLText(text))
	b.WriteString("\n</agent-message>")
	return b.String()
}

func init() {
	tool.Register(NewSendMessageTool())
}
