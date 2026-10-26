package conv

import (
	"encoding/json"
	"maps"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// AgentDisplay is UI-only metadata. ColorKey is a theme-independent palette slot.
type AgentDisplay struct {
	ID, Name string
	ColorKey int
}

// AgentDisplayName disambiguates equal names without treating names as routes.
func AgentDisplayName(d AgentDisplay, identities map[string]AgentDisplay) string {
	name := strings.Join(strings.Fields(d.Name), " ")
	if name == "" {
		return shortAgentDisplayID(d.ID)
	}
	for _, other := range identities {
		if other.ID != d.ID && other.ID != "" && strings.EqualFold(strings.Join(strings.Fields(other.Name), " "), name) {
			return name + " · " + shortAgentDisplayID(d.ID)
		}
	}
	return name
}

type agentIdentityState struct {
	session  string
	displays map[string]AgentDisplay
	next     int
	scanned  int
	lastID   string
}

// SetAgentIdentitySession clears aliases only when the conversation changes.
func (m *Model) SetAgentIdentitySession(id string) {
	if m.identities == nil || m.identities.session != id {
		m.identities = &agentIdentityState{session: id}
	}
}

func (m *Model) reserveAgentCalls(calls []core.ToolCall) {
	if m.identities == nil {
		m.identities = &agentIdentityState{}
	}
	s := m.identities
	for _, call := range calls {
		if !tool.IsAgentToolName(call.Name) || call.ID == "" {
			continue
		}
		if _, ok := s.displays[call.ID]; ok {
			continue
		}
		var args struct {
			Name   string `json:"name"`
			Resume string `json:"resume_task_id"`
		}
		_ = json.Unmarshal([]byte(call.Input), &args)
		if d, ok := s.displays[args.Resume]; args.Resume != "" && ok {
			s.put(call.ID, d)
			continue
		}
		s.put(call.ID, AgentDisplay{ID: call.ID, Name: args.Name, ColorKey: s.next % 6})
		s.next++
	}
}

func (s *agentIdentityState) put(key string, d AgentDisplay) {
	if key == "" {
		return
	}
	if old, ok := s.displays[key]; ok && old == d {
		return
	}
	s.displays = maps.Clone(s.displays)
	if s.displays == nil {
		s.displays = make(map[string]AgentDisplay)
	}
	s.displays[key] = d
}

func (m *Model) bindAgentIdentity(callID string, identity toolresult.AgentIdentity) {
	if identity.CallID != "" {
		callID = identity.CallID
	}
	if m.identities == nil {
		return
	}
	s := m.identities
	d, ok := s.displays[callID]
	if !ok {
		return
	}
	old := d
	if identity.ID != "" {
		d.ID = identity.ID
	}
	if identity.Name != "" {
		d.Name = identity.Name
	}
	for key, existing := range s.displays {
		if existing == old {
			s.put(key, d)
		}
	}
	s.put(callID, d)
	for _, key := range []string{identity.ID, identity.SessionID} {
		if existing, exists := s.displays[key]; exists && existing.ID != old.ID && existing.ID != d.ID {
			continue
		}
		s.put(key, d)
	}
}

// AgentDisplayHistorySnapshot supplements the session registry from persisted rows.
func (m *Model) AgentDisplayHistorySnapshot(messages []core.ChatMessage) map[string]AgentDisplay {
	m.AgentDisplaySnapshot()
	for _, msg := range messages {
		m.reserveAgentCalls(msg.ToolCalls)
		if d, ok := msg.ToolDetails.(toolresult.AgentDetails); ok && msg.ToolResult != nil {
			m.bindAgentIdentity(msg.ToolResult.ToolCallID, d.Identity)
		}
	}
	return m.identities.displays
}

// AgentDisplaySnapshot returns a read-only snapshot, stable until metadata changes.
func (m *Model) AgentDisplaySnapshot() map[string]AgentDisplay {
	if m.identities == nil {
		m.identities = &agentIdentityState{}
	}
	s := m.identities
	start := s.scanned
	if start > len(m.Messages) || (start > 0 && m.Messages[start-1].ID != s.lastID) {
		start = 0
	}
	if start > 0 {
		start--
	}
	// Calls within each launch reserve slots before any sibling result binds aliases.
	for _, msg := range m.Messages[start:] {
		m.reserveAgentCalls(msg.ToolCalls)
		if details, ok := msg.ToolDetails.(toolresult.AgentDetails); ok && msg.ToolResult != nil {
			m.bindAgentIdentity(msg.ToolResult.ToolCallID, details.Identity)
		}
	}
	m.reserveAgentCalls(m.Tool.PendingCalls)
	s.scanned = len(m.Messages)
	if s.scanned > 0 {
		s.lastID = m.Messages[s.scanned-1].ID
	}
	return s.displays
}
