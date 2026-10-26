package conv

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

func identityCalls() []core.ToolCall {
	return []core.ToolCall{{ID: "spawn-a", Name: "Agent", Input: `{"name":"reviewer"}`}, {ID: "spawn-b", Name: "Agent", Input: `{"name":"reviewer"}`}}
}

func TestAgentIdentityOrderingRestoration(t *testing.T) {
	m := NewModel(80)
	m.Messages = []core.ChatMessage{{ID: "launch", Role: core.ChatAssistant, ToolCalls: identityCalls()}}
	before := m.AgentDisplaySnapshot()
	for _, pair := range [][2]string{{"spawn-b", "route-b"}, {"spawn-a", "route-a"}} {
		m.Messages = append(m.Messages, core.ChatMessage{ID: pair[1], Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: pair[0]}, ToolDetails: toolresult.AgentDetails{Identity: toolresult.AgentIdentity{ID: pair[1], Name: "reviewer", CallID: pair[0], SessionID: "session-" + pair[1]}}})
	}
	got := m.AgentDisplaySnapshot()
	if got["route-a"].ColorKey != 0 || got["route-b"].ColorKey != 1 {
		t.Fatalf("completion reordered colors: %#v", got)
	}
	if before["spawn-a"].ID != "spawn-a" {
		t.Fatal("mutated published snapshot")
	}
	if got["session-route-a"] != got["spawn-a"] {
		t.Fatal("missing session alias")
	}
	if AgentDisplayName(got["route-a"], got) == "reviewer" {
		t.Fatal("duplicate name not disambiguated")
	}
	if _, ok := got["reviewer"]; ok {
		t.Fatal("name became route")
	}
	restored := NewModel(80)
	restored.Messages = m.Messages
	if restored.AgentDisplaySnapshot()["route-b"] != got["route-b"] {
		t.Fatal("restoration changed identity")
	}
	m.Messages = []core.ChatMessage{{ID: "compact", Role: core.ChatUser, Content: "summary"}}
	if m.AgentDisplaySnapshot()["route-a"] != got["route-a"] {
		t.Fatal("compaction lost identity")
	}
	m.Messages = append(m.Messages, core.ChatMessage{ID: "resume", Role: core.ChatAssistant, ToolCalls: []core.ToolCall{{ID: "resume-call", Name: "Agent", Input: `{"resume_task_id":"route-a"}`}}})
	if m.AgentDisplaySnapshot()["resume-call"] != got["route-a"] {
		t.Fatal("resume allocated identity")
	}
	reloaded := NewModel(80)
	reloaded.Messages = append(restored.Messages, m.Messages[len(m.Messages)-1])
	if reloaded.AgentDisplaySnapshot()["resume-call"] != got["route-a"] {
		t.Fatal("persisted resume allocated identity")
	}
	m.SetAgentIdentitySession("new")
	m.Messages = nil
	if len(m.AgentDisplaySnapshot()) != 0 {
		t.Fatal("new session leaked aliases")
	}
}

func TestAgentIdentityPartialFence(t *testing.T) {
	m := NewModel(80)
	m.Tool.Track(identityCalls())
	m.reserveAgentCalls(identityCalls())
	event := sdkagent.ToolUpdate{ID: "spawn-a", Name: "Agent"}
	event.Partial.Details = toolresult.AgentDetails{Identity: toolresult.AgentIdentity{ID: "route-a", CallID: "spawn-a"}}
	applyAgentEvent(nil, &m, event)
	if _, ok := m.AgentDisplaySnapshot()["route-a"]; ok {
		t.Fatal("unstarted partial accepted")
	}
	applyToolRunning(&m, "spawn-a", "Agent")
	started := m.Tool.StartedAt["spawn-a"]
	applyAgentEvent(nil, &m, event)
	if m.AgentDisplaySnapshot()["route-a"].ID != "route-a" {
		t.Fatal("live partial ignored")
	}
	if !m.Tool.StartedAt["spawn-a"].Equal(started) {
		t.Fatal("partial reset timer")
	}
	m.Tool.MarkComplete("spawn-a")
	completedTimer := m.Tool.StartedAt["spawn-a"]
	event.Partial.Details = toolresult.AgentDetails{Identity: toolresult.AgentIdentity{ID: "late", CallID: "spawn-a"}}
	applyAgentEvent(nil, &m, event)
	if _, ok := m.AgentDisplaySnapshot()["late"]; ok {
		t.Fatal("completed partial accepted")
	}
	if !m.Tool.StartedAt["spawn-a"].Equal(completedTimer) {
		t.Fatal("completed timer restarted")
	}
}

func TestCompletionSpacing(t *testing.T) {
	messages := []core.ChatMessage{
		{Role: core.ChatAssistant, Content: "Launch"},
		{Role: core.ChatUser, Content: `<task-notification task-id="a" status="completed">Done</task-notification>`},
		{Role: core.ChatUser, Content: `<task-notification task-id="b" status="completed">Done</task-notification>`},
		{Role: core.ChatUser, Content: "Next"},
	}
	p := RenderContext{Messages: messages, Width: 80}
	if !strings.HasPrefix(RenderMessageAt(p, 1, false), "\n") {
		t.Fatal("first notice needs group boundary")
	}
	if strings.HasPrefix(RenderMessageAt(p, 2, false), "\n") {
		t.Fatal("consecutive notice gained blank row")
	}
	if !strings.HasPrefix(RenderMessageAt(p, 3, false), "\n") {
		t.Fatal("normal user boundary lost")
	}
	p.Messages[1].Expanded = true
	if !strings.HasPrefix(RenderMessageAt(p, 2, false), "\n") {
		t.Fatal("expanded notice boundary lost")
	}
}
