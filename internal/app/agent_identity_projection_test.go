package app

import (
	"context"
	"fmt"
	"maps"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func agentIdentityProjectionMessages(prefix, route, sessionID string) []core.ChatMessage {
	callID := prefix + "-call"
	return []core.ChatMessage{
		{ID: prefix + "-launch", Role: core.ChatAssistant, ToolCalls: []core.ToolCall{{ID: callID, Name: "Agent", Input: `{"name":"reviewer"}`}}},
		{ID: prefix + "-result", Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: callID, ToolName: "Agent"}, ToolDetails: toolresult.AgentDetails{Identity: toolresult.AgentIdentity{ID: route, Name: "reviewer", CallID: callID, SessionID: sessionID}}},
	}
}

func TestAgentIdentityRunningBackgroundSurvivesCompaction(t *testing.T) {
	for _, nativeSessionOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("native-session-only=%t", nativeSessionOnly), func(t *testing.T) {
			m := fixedComposerModel(80, 24)
			m.services.Task = task.NewManager()
			worker := m.services.Task.CreateAgentTask("running-task", "reviewer", "Review", context.Background(), nil)
			worker.SetIdentity("reviewer", "native-session")
			t.Cleanup(func() { worker.Complete(nil) })
			route := "running-task"
			if nativeSessionOnly {
				route = "persisted-route"
			}
			m.conv.Messages = agentIdentityProjectionMessages("background", route, "native-session")
			original := m.messageRenderParams().AgentIdentities[route]
			if original.ID != route {
				t.Fatal("background identity was not registered")
			}

			// Compaction removes transcript references, but the worker is still running.
			m.conv.Messages = nil
			if got := m.messageRenderParams().AgentIdentities["native-session"]; got != original {
				t.Fatalf("compaction changed background identity: %#v", got)
			}
			// Each batch replaces the transcript, making previous aliases cold.
			churn := func(prefix string) {
				for i := range 600 {
					id := fmt.Sprintf("%s-%d", prefix, i)
					m.conv.Messages = agentIdentityProjectionMessages(id, id+"-route", id+"-session")
					_ = m.messageRenderParams()
				}
				m.conv.Messages = nil
			}
			churn("while-running")
			live := m.messageRenderParams().AgentIdentities
			for _, key := range []string{route, "native-session"} {
				if live[key] != original {
					t.Fatalf("running identity %q lost after cache pressure: %#v", key, live[key])
				}
			}
			if len(live) > 514 {
				t.Fatalf("cold aliases were not bounded: %d", len(live))
			}
			worker.Complete(nil)
			churn("pending-completion-notice")
			pending := m.messageRenderParams().AgentIdentities
			for _, key := range []string{route, "native-session"} {
				if pending[key] != original {
					t.Fatalf("terminal identity %q lost before completion notice delivery: %#v", key, pending[key])
				}
			}
			info := worker.GetStatus()
			m.taskCompletionsShown = map[string]bool{taskCompletionKey(info.ID, info.Attempt): true}
			churn("after-completion-notice")
			for _, key := range []string{route, "native-session", "background-call"} {
				if _, ok := m.messageRenderParams().AgentIdentities[key]; ok {
					t.Fatalf("completed identity %q remained pinned", key)
				}
			}
		})
	}
}

func TestAgentIdentityCompletionRetainsRecentAliases(t *testing.T) {
	m := fixedComposerModel(80, 24)
	m.services.Task = task.NewManager()
	worker := m.services.Task.CreateAgentTask("recent-task", "reviewer", "Review", context.Background(), nil)
	worker.SetIdentity("reviewer", "recent-session")
	t.Cleanup(func() { worker.Complete(nil) })
	m.conv.Messages = agentIdentityProjectionMessages("recent", "recent-task", "recent-session")
	before := maps.Clone(m.messageRenderParams().AgentIdentities)
	m.conv.Messages = nil
	worker.Complete(nil)
	info := worker.GetStatus()
	m.taskCompletionsShown = map[string]bool{taskCompletionKey(info.ID, info.Attempt): true}
	if after := m.messageRenderParams().AgentIdentities; !maps.Equal(before, after) {
		t.Fatalf("completion prematurely evicted recent aliases: before=%#v after=%#v", before, after)
	}
}

func TestAgentIdentityUnrelatedHistoryDoesNotMutateLiveRegistry(t *testing.T) {
	m := fixedComposerModel(80, 24)
	m.conv.Messages = agentIdentityProjectionMessages("live", "live-route", "live-session")
	before := maps.Clone(m.messageRenderParams().AgentIdentities)
	var history []core.ChatMessage
	for i := range 600 {
		id := fmt.Sprintf("history-%d", i)
		history = append(history, agentIdentityProjectionMessages(id, id+"-route", id+"-session")...)
	}
	if entries := m.historyEntries(history); len(entries) == 0 {
		t.Fatal("unrelated history produced no entries")
	}
	if after := m.messageRenderParams().AgentIdentities; !maps.Equal(before, after) {
		t.Fatalf("history changed live registry size or colors: before=%#v after=%#v", before, after)
	}
	// A detached history projection must not advance live palette assignment either.
	control := fixedComposerModel(80, 24)
	control.conv.Messages = agentIdentityProjectionMessages("live", "live-route", "live-session")
	_ = control.messageRenderParams()
	next := agentIdentityProjectionMessages("next", "next-route", "next-session")
	m.conv.Messages = append(m.conv.Messages, next...)
	control.conv.Messages = append(control.conv.Messages, next...)
	if got, want := m.messageRenderParams().AgentIdentities["next-route"], control.messageRenderParams().AgentIdentities["next-route"]; got != want {
		t.Fatalf("history advanced live palette: got=%#v want=%#v", got, want)
	}
}

func TestAgentIdentityRootProjection(t *testing.T) {
	m := fixedComposerModel(80, 24)
	m.conv.Messages = []core.ChatMessage{
		{ID: "launch", Role: core.ChatAssistant, ToolCalls: []core.ToolCall{{ID: "spawn", Name: "Agent", Input: `{"name":"reviewer"}`}}},
		{ID: "done", Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "spawn", ToolName: "Agent"}, ToolDetails: toolresult.AgentDetails{Identity: toolresult.AgentIdentity{ID: "route", Name: "reviewer", CallID: "spawn", SessionID: "child"}}},
	}
	first := m.messageRenderParams().AgentIdentities
	if first["route"].ID != "route" || first["child"] != first["spawn"] {
		t.Fatalf("missing root aliases: %#v", first)
	}
	_ = m.historyEntries(m.conv.Messages)
	second := m.messageRenderParams().AgentIdentities
	if second["route"] != first["route"] {
		t.Fatal("history changed live projection")
	}
}
