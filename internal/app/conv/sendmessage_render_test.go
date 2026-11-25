package conv

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
)

func TestSendMessageBrokerReceiptIsInline(t *testing.T) {
	call := core.ToolCall{ID: "message", Name: tool.ToolSendMessage, Input: `{"to":"worker-1","subject":"Finalize metadata\nbefore publication"}`}
	params := ToolCallsParams{
		ToolCalls:       []core.ToolCall{call},
		Width:           80,
		AgentIdentities: map[string]AgentDisplay{"worker-1": {ID: "worker-1", Name: "gateway-openapi"}},
	}
	if got := ansi.Strip(RenderToolCalls(params)); got != "● To gateway-openapi: Finalize metadata before publication\n" {
		t.Fatalf("pending call should not claim that it was queued: %q", got)
	}
	params.ResultMap = map[string]ToolResultData{
		call.ID: {ToolName: call.Name, Content: "Message accepted and queued for gateway-openapi (task worker-1); the recipient reads queued messages at its next step."},
	}
	if got := ansi.Strip(RenderToolCalls(params)); got != "● To gateway-openapi: Finalize metadata before publication · Queued\n" {
		t.Fatalf("expected one message row with a queue status: %q", got)
	}
	for _, width := range []int{24, 32, 40} {
		params.Width = width
		got := ansi.Strip(RenderToolCalls(params))
		if !strings.HasSuffix(got, " · Queued\n") || strings.Contains(got, ": ·") || strings.Count(got, "\n") != 1 || ansi.StringWidth(strings.TrimSuffix(got, "\n")) > width {
			t.Errorf("width %d: the recipient and status should stay on one clean row: %q", width, got)
		}
	}
	params.Width = 80
	params.ToolCalls[0].Input = `{"to":"worker-1","message":"internal body"}`
	if got := ansi.Strip(RenderToolCalls(params)); got != "● To gateway-openapi · Queued\n" {
		t.Errorf("subjectless message should show recipient and status only: %q", got)
	}
}

func TestSendMessageKeepsOtherResultsAndExpandedReceipt(t *testing.T) {
	call := core.ToolCall{ID: "message", Name: tool.ToolSendMessage, Input: `{"to":"worker-1","subject":"Follow up"}`}
	for _, tc := range []struct {
		name    string
		content string
		want    string
		isError bool
		expand  bool
	}{
		{name: "failed", content: "Error: worker stopped", want: "worker stopped", isError: true},
		{name: "continuation", content: "Message accepted; agent worker-1 started its next execution in the original session.", want: "Message accepted"},
		{name: "expanded continuation", content: "Message accepted; agent worker-1 started its next execution in the original session.", want: "started its next execution", expand: true},
		{name: "group offline", content: "Queued · offline; @worker-1 sees it when its session resumes.", want: "offline"},
		{name: "expanded receipt", content: "Message accepted and queued for worker-1; the recipient reads it at its next step.", want: "reads it at its next step", expand: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ansi.Strip(RenderToolCalls(ToolCallsParams{
				ToolCalls: []core.ToolCall{call}, Width: 120,
				ResultMap: map[string]ToolResultData{
					call.ID: {ToolName: call.Name, Content: tc.content, IsError: tc.isError, Expanded: tc.expand},
				},
			}))
			heading, result, _ := strings.Cut(got, "\n")
			if strings.Contains(heading, " · Queued") || !strings.Contains(result, tc.want) {
				t.Fatalf("result should stay inspectable instead of folding into Queued: %q", got)
			}
		})
	}
}
