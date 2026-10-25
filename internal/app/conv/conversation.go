package conv

import (
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type StreamState struct {
	MessageID    string // Inference output belongs to this message, independent of display order.
	Active       bool
	Waiting      bool      // Submission accepted by the UI; the first inference has not started.
	StartedAt    time.Time // When the current wait for the model began; times the waiting row.
	BuildingTool string
	// Inferring is narrower than Active: a turn stays active while its tools
	// run, but generation progress must stop at InferenceEnd.
	Inferring      bool
	Attempt        int
	GeneratingTool string
	GeneratedBytes int
	// ThinkingActive follows the latest text/thinking delta. Responses may
	// return to reasoning after commentary in the same assistant message.
	ThinkingActive bool
}

func (s *StreamState) Stop() {
	s.MessageID = ""
	s.Active = false
	s.Waiting = false
	s.BuildingTool = ""
	s.EndInference()
	s.Attempt = 0
	s.ThinkingActive = false
}

func (s *StreamState) EndInference() {
	s.Inferring = false
	s.GeneratingTool = ""
	s.GeneratedBytes = 0
}

type ConversationModel struct {
	Messages       []core.ChatMessage
	CommittedCount int
	Stream         StreamState
	Compact        CompactState
	Modal          ModalState
	Tool           ToolExecState

	// OnNotice sees every notice appended, so the host can keep it in the
	// transcript; a notice never reaches the model, only the person.
	OnNotice func(core.ChatMessage)
}

func NewConversation() ConversationModel {
	return ConversationModel{
		Messages: []core.ChatMessage{},
		Modal:    NewModalState(),
	}
}

func (m *ConversationModel) Append(msg core.ChatMessage) core.ChatMessage {
	// Stamp an ID once at append time so subsequent transcript saves can
	// dedupe by it. Without this, every save assigns a fresh UUID and the
	// append-only persistence path re-writes the entire history each turn.
	if msg.ID == "" {
		msg.ID = core.NewMessageID()
	}
	m.Messages = append(m.Messages, msg)
	if msg.Role == core.ChatNotice && m.OnNotice != nil {
		m.OnNotice(msg)
	}
	return msg
}

func (m *ConversationModel) Clear() {
	m.Stream.Stop()
	m.Messages = []core.ChatMessage{}
	m.CommittedCount = 0
}

func (m *ConversationModel) AddNotice(content string) {
	m.Append(core.ChatMessage{Role: core.ChatNotice, Content: content})
}

// AddAgentNotice adds a notice that came from a background agent (a subagent
// completion or interim report), rendered distinctly from a plain system notice.
func (m *ConversationModel) AddAgentNotice(content string) {
	m.Append(core.ChatMessage{Role: core.ChatNotice, Content: content, AgentNotice: true})
}

func (m *ConversationModel) StreamMessageIndex() int {
	if m.Stream.MessageID == "" {
		return -1
	}
	for i := len(m.Messages) - 1; i >= 0; i-- {
		if m.Messages[i].ID == m.Stream.MessageID && m.Messages[i].Role == core.ChatAssistant {
			return i
		}
	}
	return -1
}

func (m *ConversationModel) AppendStream(text, thinking string) {
	idx := m.StreamMessageIndex()
	if idx < 0 {
		return
	}
	if thinking != "" {
		m.Stream.ThinkingActive = true
		m.Messages[idx].AppendThinking(thinking, time.Now())
	}
	if text != "" {
		m.Stream.ThinkingActive = false
		m.Messages[idx].Content += text
	}
}

func (m *ConversationModel) SetStreamToolCalls(calls []core.ToolCall) {
	idx := m.StreamMessageIndex()
	if idx < 0 {
		return
	}
	m.Messages[idx].ToolCalls = calls
}

func (m *ConversationModel) SetStreamThinkingSignature(sig string) {
	idx := m.StreamMessageIndex()
	if sig == "" || idx < 0 {
		return
	}
	m.Messages[idx].ThinkingSignature = sig
}

// SetLastAutopilotNote hangs a "⎿ autopilot · <note>" mark under the last
// message when it is a user turn.
func (m *ConversationModel) SetLastAutopilotNote(note string) {
	if len(m.Messages) == 0 {
		return
	}
	last := &m.Messages[len(m.Messages)-1]
	if last.Role != core.ChatUser {
		return
	}
	last.AutopilotNote = note
}

func (m *ConversationModel) AppendCancelledToolResults(calls []core.ToolCall, contentFn func(core.ToolCall) string, decisionFn func(callID string) *core.ReviewDecision) {
	for _, tc := range calls {
		m.Append(core.ChatMessage{
			Role: core.ChatUser,
			ToolResult: &core.ToolResult{
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    ai.TextContent(contentFn(tc)),
				IsError:    true,
			},
			// Consume the stashed auto-review decision so an interrupted judged
			// call still shows its annotation and its handoff entry is released
			// (this synthetic result never reaches the applyPostTool path).
			Decision: decisionFn(tc.ID),
		})
	}
}

// DropStreamingAssistant removes the message that the in-flight
// (now failed) inference was streaming into, so a retry re-streams onto a clean
// tail instead of duplicating or interleaving partial output. Only uncommitted
// rows are eligible, so committed scrollback is never touched.
func (m *ConversationModel) DropStreamingAssistant() {
	idx := m.StreamMessageIndex()
	if idx < m.CommittedCount {
		return
	}
	m.Messages = append(m.Messages[:idx], m.Messages[idx+1:]...)
	m.Stream.MessageID = ""
}

func (m *ConversationModel) MarkStreamInterrupted() {
	idx := m.StreamMessageIndex()
	if idx < 0 {
		return
	}
	msg := &m.Messages[idx]
	if len(msg.ToolCalls) == 0 {
		if msg.Content == "" {
			msg.Content = InterruptedMarker
		} else {
			msg.Content += " " + InterruptedMarker
		}
	}
}

// Toggling only touches the live (uncommitted) tail: committed messages are
// already in the terminal's native scrollback and can't be re-rendered in
// place, so they stay frozen as last drawn.

func (m *ConversationModel) ToggleMostRecentExpandable() {
	for i := len(m.Messages) - 1; i >= m.CommittedCount; i-- {
		msg := &m.Messages[i]
		switch {
		case msg.ToolResult != nil:
			msg.Expanded = !msg.Expanded
			return
		case len(msg.ToolCalls) > 0:
			msg.ToolCallsExpanded = !msg.ToolCallsExpanded
			return
		}
	}
}

func (m *ConversationModel) ToggleAllExpandable() {
	anyExpanded := false
	for i := m.CommittedCount; i < len(m.Messages); i++ {
		msg := m.Messages[i]
		if (msg.ToolResult != nil && msg.Expanded) ||
			(len(msg.ToolCalls) > 0 && msg.ToolCallsExpanded) {
			anyExpanded = true
			break
		}
	}
	for i := m.CommittedCount; i < len(m.Messages); i++ {
		if m.Messages[i].ToolResult != nil {
			m.Messages[i].Expanded = !anyExpanded
		}
		if len(m.Messages[i].ToolCalls) > 0 {
			m.Messages[i].ToolCallsExpanded = !anyExpanded
		}
	}
}

func (m ConversationModel) ConvertToProvider() []core.Message {
	providerMsgs := make([]core.Message, 0, len(m.Messages))
	for _, msg := range m.Messages {
		// A notice is San talking to the person, not a turn. ToMessage is what
		// knows that, so this loop does not have to.
		if pm, ok := msg.ToMessage(); ok {
			providerMsgs = append(providerMsgs, pm)
		}
	}
	return providerMsgs
}
