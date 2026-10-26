package conv

import (
	"fmt"
	"maps"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

func cachedAgentMessages(start, count int) []core.ChatMessage {
	messages := make([]core.ChatMessage, 0, count*2)
	for i := start; i < start+count; i++ {
		callID, route := fmt.Sprintf("call-%d", i), fmt.Sprintf("route-%d", i)
		messages = append(messages,
			core.ChatMessage{ID: callID, Role: core.ChatAssistant, ToolCalls: []core.ToolCall{{ID: callID, Name: "Agent", Input: `{"name":"reviewer"}`}}},
			core.ChatMessage{ID: route, Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: callID, ToolName: "Agent"}, ToolDetails: toolresult.AgentDetails{Identity: toolresult.AgentIdentity{ID: route, CallID: callID, Name: "reviewer", SessionID: "session-" + route}}},
		)
	}
	return messages
}

func TestAgentIdentityCacheCompactionBound(t *testing.T) {
	m := NewModel(80)
	m.Messages = cachedAgentMessages(0, maxCachedAgentAliases)
	before := m.AgentDisplaySnapshot()
	if len(before) != 3*maxCachedAgentAliases {
		t.Fatalf("current references evicted: %d", len(before))
	}
	original := maps.Clone(before)
	m.Messages = nil
	after := m.AgentDisplaySnapshot()
	if len(after) > maxCachedAgentAliases || len(m.identities.order) != len(after) {
		t.Fatalf("compaction retained oversized cache: aliases=%d order=%d", len(after), len(m.identities.order))
	}
	if !maps.Equal(before, original) {
		t.Fatal("pruning mutated a published snapshot")
	}
	for batch := 1; batch <= 20; batch++ {
		m.Messages = cachedAgentMessages(batch*20+1000, 20)
		m.AgentDisplaySnapshot()
		m.Messages = nil
		if got := len(m.AgentDisplaySnapshot()); got > maxCachedAgentAliases {
			t.Fatalf("long session grew cache at batch %d: %d", batch, got)
		}
	}
	m.SetAgentIdentitySession("replacement")
	if s := m.identities; len(s.displays) != 0 || len(s.order) != 0 || len(s.active) != 0 || s.next != 0 {
		t.Fatalf("session switch retained identity state: %#v", s)
	}
}

func TestAgentIdentityCacheProtectsReferences(t *testing.T) {
	m := NewModel(80)
	m.Messages = cachedAgentMessages(0, 5)
	before := m.AgentDisplaySnapshot()
	m.SetActiveAgentIdentities([]string{"route-0", "session-route-0"})
	m.Messages = cachedAgentMessages(100, maxCachedAgentAliases)
	m.AgentDisplaySnapshot()
	m.Messages = []core.ChatMessage{
		{ID: "mail", Role: core.ChatAssistant, ToolCalls: []core.ToolCall{{ID: "mail", Name: "SendMessage", Input: `{"to":"route-1"}`}}},
		{ID: "notice", Role: core.ChatUser, Content: `<task-notification task-id="route-2" status="completed"/>`},
		{ID: "sender", Role: core.ChatUser, Content: `<agent-message from="session-route-3">Hello</agent-message>`},
	}
	m.Tool.Track([]core.ToolCall{{ID: "pending-resume", Name: "Agent", Input: `{"resume_task_id":"route-4"}`}})
	after := m.AgentDisplaySnapshot()
	for _, id := range []string{"route-0", "session-route-0", "route-1", "route-2", "session-route-3", "route-3", "route-4", "pending-resume"} {
		if _, exists := after[id]; !exists {
			t.Fatalf("referenced alias evicted: %s", id)
		}
	}
	if after["route-0"] != before["route-0"] || after["pending-resume"] != before["route-4"] {
		t.Fatal("active or resumed agent changed color")
	}
	if len(after) > maxCachedAgentAliases+8 {
		t.Fatalf("unreferenced aliases grew beyond allowance: %d", len(after))
	}
	m.Tool.Reset()
	m.Messages = nil
	m.SetActiveAgentIdentities(nil)
	m.AgentDisplaySnapshot()
	if _, exists := m.AgentDisplaySnapshot()["route-0"]; exists {
		t.Fatal("unpinned old agent never became evictable")
	}
}

func TestAgentIdentityResumeAfterLaunchAliasEviction(t *testing.T) {
	m := NewModel(80)
	m.Messages = cachedAgentMessages(0, 1)
	original := m.AgentDisplaySnapshot()["route-0"]
	m.SetActiveAgentIdentities([]string{"route-0"})
	m.Messages = cachedAgentMessages(100, maxCachedAgentAliases)
	m.AgentDisplaySnapshot()
	m.Messages = nil
	if _, exists := m.AgentDisplaySnapshot()["call-0"]; exists {
		t.Fatal("obsolete launch alias should be evictable")
	}
	m.Tool.Track([]core.ToolCall{{ID: "resume", Name: "Agent", Input: `{"resume_task_id":"route-0"}`}})
	applyToolRunning(&m, "resume", "Agent")
	event := sdkagent.ToolUpdate{ID: "resume", Name: "Agent"}
	event.Partial.Details = toolresult.AgentDetails{Identity: toolresult.AgentIdentity{ID: "route-0", CallID: "call-0", SessionID: "new-native"}}
	applyAgentEvent(nil, &m, event)
	if got := m.AgentDisplaySnapshot(); got["resume"] != original || got["new-native"] != original {
		t.Fatalf("continuation lost identity after pruning: %#v", got)
	}
}

func TestAgentIdentityHistoryDetached(t *testing.T) {
	m := NewModel(80)
	m.Messages = cachedAgentMessages(0, 1)
	before := m.AgentDisplaySnapshot()
	original := maps.Clone(before)
	next, scanned, order := m.identities.next, m.identities.scanned, len(m.identities.order)
	for range 3 {
		history := m.AgentDisplayHistorySnapshot(cachedAgentMessages(100, maxCachedAgentAliases))
		if _, exists := history["route-100"]; !exists {
			t.Fatal("detached history lacks persisted identity")
		}
		if !maps.Equal(m.identities.displays, original) || m.identities.next != next || m.identities.scanned != scanned || len(m.identities.order) != order {
			t.Fatal("opening history changed live cache")
		}
	}
	m.Messages = append(m.Messages, cachedAgentMessages(1, 1)...)
	if m.AgentDisplaySnapshot()["route-1"].ColorKey != 1 {
		t.Fatal("history consumed live palette slots")
	}
	if !maps.Equal(before, original) {
		t.Fatal("later writes mutated published map")
	}
}

func TestAgentIdentityControlCallsDoNotAllocate(t *testing.T) {
	m := NewModel(80)
	m.reserveAgentCalls([]core.ToolCall{
		{ID: "mail", Name: "SendMessage", Input: `{"to":"unknown"}`},
		{ID: "stop", Name: "AgentStop", Input: `{"task_id":"unknown"}`},
		{ID: "spawn", Name: "Agent", Input: `{"name":"worker"}`},
	})
	if got := m.AgentDisplaySnapshot(); len(got) != 1 || got["spawn"].ColorKey != 0 {
		t.Fatalf("control calls allocated agent identities: %#v", got)
	}
}
