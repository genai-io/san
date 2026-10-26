package conv

import (
	"encoding/json"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// Current references are retained separately from this allowance for old aliases.
const maxCachedAgentAliases = 512

func (m *Model) pruneAgentIdentities() {
	s := m.identities
	if !s.dirty {
		return
	}
	s.dirty = false
	if len(s.displays) <= maxCachedAgentAliases {
		return
	}
	keep := make(map[string]bool)
	protect := func(id string) {
		if d, exists := s.displays[id]; exists {
			s.put(d.ID, d)
			keep[id] = true
			keep[d.ID] = true
		}
	}
	protectCalls := func(calls []core.ToolCall) {
		for _, call := range calls {
			protect(call.ID)
			if !tool.IsAgentToolName(call.Name) {
				continue
			}
			var args struct {
				Resume string `json:"resume_task_id"`
				To     string `json:"to"`
				TaskID string `json:"task_id"`
			}
			_ = json.Unmarshal([]byte(call.Input), &args)
			protect(args.Resume)
			protect(args.To)
			protect(args.TaskID)
		}
	}
	for _, id := range s.active {
		protect(id)
	}
	protectCalls(m.Tool.PendingCalls)
	for _, msg := range m.Messages {
		protectCalls(msg.ToolCalls)
		if msg.ToolResult != nil {
			protect(msg.ToolResult.ToolCallID)
		}
		if d, ok := msg.ToolDetails.(toolresult.AgentDetails); ok {
			protect(d.Identity.ID)
			protect(d.Identity.CallID)
			protect(d.Identity.SessionID)
			protect(d.TaskID)
		}
		if IsAgentEnvelope(msg.Content) {
			tag, _, _ := strings.Cut(strings.TrimSpace(msg.Content), ">")
			for _, attr := range []string{"task-id", "agent-id", "from"} {
				protect(envelopeAttr(tag, attr))
			}
		}
	}
	remaining := maxCachedAgentAliases
	for i := len(s.order) - 1; i >= 0; i-- {
		key := s.order[i]
		if !keep[key] && remaining > 0 {
			keep[key] = true
			remaining--
		}
	}
	if len(keep) == len(s.displays) {
		return
	}
	// Rebuild both containers so removed strings and excess capacity can be collected.
	displays := make(map[string]AgentDisplay, len(keep))
	order := make([]string, 0, len(keep))
	for _, key := range s.order {
		if keep[key] {
			displays[key] = s.displays[key]
			order = append(order, key)
		}
	}
	s.displays, s.order, s.shared = displays, order, false
}
