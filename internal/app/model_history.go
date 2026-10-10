package app

import (
	"errors"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
)

type historyLoadedMsg struct {
	generation uint64
	sessionID  string
	messages   []core.ChatMessage
	err        error
}

func (m *model) openHistory() tea.Cmd {
	m.userInput.Transcript.Enter("History · Loading…", nil, m.env.Width, m.env.Height)
	msg := historyLoadedMsg{generation: m.userInput.Transcript.Generation(), messages: slices.Clone(m.conv.Messages)}
	var load func(string) ([]core.ChatMessage, error)
	if m.services.Session != nil {
		msg.sessionID = m.services.Session.ID()
		if store := m.services.Session.GetStore(); store != nil && msg.sessionID != "" {
			load = store.LoadHistory
		}
	}
	return func() tea.Msg {
		if load != nil {
			messages, err := load(msg.sessionID)
			if err == nil {
				msg.messages = messages
			} else if !errors.Is(err, os.ErrNotExist) {
				msg.err = err
			}
		}
		return msg
	}
}

func (m *model) historyLoaded(msg historyLoadedMsg) {
	viewer := &m.userInput.Transcript
	if !viewer.IsActive() || viewer.Generation() != msg.generation {
		return
	}
	if m.services.Session != nil && m.services.Session.ID() != msg.sessionID {
		return
	}
	if msg.err != nil {
		viewer.Enter("History", []string{"Unable to read session history: " + msg.err.Error()}, m.env.Width, m.env.Height)
		return
	}
	entries := m.historyEntries(msg.messages)
	if len(entries) == 0 {
		viewer.Enter("History", []string{"No messages yet."}, m.env.Width, m.env.Height)
		return
	}
	viewer.EnterEntries(entries, m.env.Width, m.env.Height)
}

// historyEntries reuses the conversation renderers with settled, full messages.
// Each tool gets its own entry so parallel results expand independently.
func (m *model) historyEntries(messages []core.ChatMessage) []input.TranscriptEntry {
	params := conv.RenderContext{
		Width: m.env.Width, MDRenderer: conv.NewMDRenderer(m.env.Width),
		ThinkingDisplay: m.env.ThinkingDisplay,
		AgentColors:     m.agentColors(), MemberColors: m.grp.colors,
		TaskOwnerMap: buildTaskOwnerMap(m.services.Tracker.List()),
	}
	renderer := func(width int) *conv.MDRenderer {
		if params.Width != width {
			params.Width = width
			params.MDRenderer = conv.NewMDRenderer(width)
		}
		return params.MDRenderer
	}
	results := make(map[string]conv.ToolResultData)
	owned := make(map[string]bool)
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			owned[call.ID] = true
		}
		if r := msg.ToolResult; r != nil {
			results[r.ToolCallID] = conv.ToolResultData{
				ToolName: r.ToolName, Content: r.Content.Text(), IsError: r.IsError,
				Details: msg.ToolDetails, Decision: msg.Decision,
			}
		}
	}
	var entries []input.TranscriptEntry
	for _, msg := range messages {
		if r := msg.ToolResult; r != nil {
			if !owned[r.ToolCallID] {
				data := results[r.ToolCallID]
				entries = append(entries, input.TranscriptEntry{Label: r.ToolName, Tool: true, Render: func(width int, expanded bool) string {
					data.Width, data.Expanded = width, expanded
					return conv.RenderToolResultInline(data, renderer(width))
				}})
			}
			continue
		}
		calls := msg.ToolCalls
		msg.ToolCalls = nil
		msg.ContentCommittedLen, msg.ThinkingCommittedLen = 0, 0
		msg.BulletEmitted, msg.ThinkingEmitted = false, false
		if msg.Content != "" || msg.Thinking != "" || len(msg.Images) > 0 {
			entries = append(entries, input.TranscriptEntry{Render: func(width int, _ bool) string {
				p := params
				p.Width, p.MDRenderer = width, renderer(width)
				p.Messages = []core.ChatMessage{msg}
				return conv.RenderSingleMessage(p, 0)
			}})
		}
		for _, call := range calls {
			data, hasResult := results[call.ID]
			render := func(width int, expanded bool) string {
				resultMap := make(map[string]conv.ToolResultData)
				if hasResult {
					data.Expanded = expanded
					resultMap[call.ID] = data
				}
				return conv.RenderToolCalls(conv.ToolCallsParams{
					ToolCalls: []core.ToolCall{call}, ResultMap: resultMap,
					Width: width, MDRenderer: renderer(width),
					AgentColors: params.AgentColors, MemberColors: params.MemberColors,
					TaskOwnerMap: params.TaskOwnerMap,
				})
			}
			// Tracker-only tools deliberately draw no conversation row.
			if strings.TrimSpace(render(m.env.Width, false)) != "" {
				entries = append(entries, input.TranscriptEntry{Label: call.Name, Tool: true, Render: render})
			}
		}
	}
	return entries
}
