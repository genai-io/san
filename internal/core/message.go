package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// NewMessageID returns a fresh short hex identifier for a Message.
// 8 bytes (16 hex chars) — collision space is large enough for the
// per-session message volume we ever see; brevity matters because the
// ID appears in every transcript record's id field.
func NewMessageID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Role identifies who produced a message in the conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool_result" // a tool-result turn; "tool_result" is its wire value
	RoleNotice    Role = "notice"
)

// Signal represents control signals sent through channels.
type Signal string

const (
	SigStop Signal = "stop"
	// SigCompact asks the agent to compact in place using a precomputed
	// summary carried in the message Content. Handled at a phase boundary on
	// the agent's own goroutine, so it never races the conversation chain.
	SigCompact Signal = "compact"
)

// Message is the wire/agent-chain message: the unit the LLM provider and the
// agent run loop exchange, append to history, and persist. It holds no
// UI/display state — for the rendered TUI view-model, see ChatMessage.
type Message struct {
	ID                string         `json:"id,omitempty"`
	Role              Role           `json:"role"`
	Content           string         `json:"content,omitempty"`
	DisplayContent    string         `json:"display_content,omitempty"`
	Images            []Image        `json:"images,omitempty"`
	Thinking          string         `json:"thinking,omitempty"`
	ThinkingSignature string         `json:"thinking_signature,omitempty"`
	ToolCalls         []ToolCall     `json:"tool_calls,omitempty"`
	ToolResult        *ToolResult    `json:"tool_result,omitempty"`
	From              string         `json:"from,omitempty"`
	Signal            Signal         `json:"-"`
	Meta              map[string]any `json:"meta,omitempty"`
}

// ChatMessage is the TUI view-model for one conversation entry: the same
// content as Message plus transient display state (the expand/collapse
// toggles). The app layer renders ChatMessages and converts them back to
// Message before sending to the provider — see
// conv.ConversationModel.ConvertToProvider.
//
// The tool's name lives on ToolResult.ToolName (the single source of truth),
// not on the ChatMessage itself.
type ChatMessage struct {
	// ID is a stable per-message identifier assigned once at construction.
	// The session.Save path uses it to dedupe appends, so it must not change
	// across saves of the same message — empty IDs would trigger re-appends
	// of the entire conversation on every persist.
	ID                string
	Role              Role
	Content           string
	DisplayContent    string
	Thinking          string
	ThinkingSignature string
	Images            []Image
	ToolCalls         []ToolCall
	ToolResult        *ToolResult
	ToolCallsExpanded bool // UI: the assistant's tool-call block is expanded
	Expanded          bool // UI: the tool-result block is expanded
}

// Image represents an image attachment.
type Image struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	FileName  string `json:"file_name"`
	Size      int    `json:"size"`
}

// ToolCall represents a tool call from the model.
type ToolCall struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Input            string `json:"input"`
	ThoughtSignature []byte `json:"thought_signature,omitempty"` // Google Gemini: opaque signature to echo back
}

// ToolResult is the outcome of a tool execution.
type ToolResult struct {
	ToolCallID   string `json:"tool_call_id"`
	ToolName     string `json:"tool_name,omitempty"`
	Content      string `json:"content"`
	IsError      bool   `json:"is_error,omitempty"`
	HookResponse any    `json:"-"`
}

// --- Constructors ---

// UserMessage creates a user message with optional images.
func UserMessage(text string, images []Image) Message {
	return Message{
		Role:           RoleUser,
		Content:        text,
		DisplayContent: text,
		Images:         images,
	}
}

// AssistantMessage creates an assistant message.
func AssistantMessage(text, thinking string, calls []ToolCall) Message {
	return Message{
		Role:      RoleAssistant,
		Content:   text,
		Thinking:  thinking,
		ToolCalls: calls,
	}
}

// ErrorResult creates an error ToolResult for a tool call.
func ErrorResult(tc ToolCall, content string) *ToolResult {
	return &ToolResult{
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		Content:    content,
		IsError:    true,
	}
}

// ToolResultMessage creates a tool result message.
func ToolResultMessage(result ToolResult) Message {
	return Message{
		Role:       RoleUser,
		ToolResult: &result,
	}
}

// --- Utilities ---

// ParseToolInput deserializes JSON tool input into a params map.
func ParseToolInput(input string) (map[string]any, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return map[string]any{}, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return nil, err
	}
	return params, nil
}

const (
	systemReminderOpen  = "<system-reminder"
	systemReminderClose = "</system-reminder>"
)

// stripSystemReminders removes the trailing run of harness <system-reminder>
// blocks from user message content. AttachToContent always appends reminders
// after the user's own text, so we peel whole blocks off the end: while the
// right-trimmed content ends with a closing tag, cut back to the last opening
// tag.
//
// Anchoring on the last *opening* tag (not a regex over the merged text) is
// what makes this robust. A closing tag "</system-reminder>" never contains
// the opening-tag prefix "<system-reminder", so a reminder body that happens
// to include the literal "</system-reminder>" is still removed in full; and a
// <system-reminder> the user typed mid-message (followed by their own prose)
// is left untouched, because once the trailing block is peeled the remaining
// text no longer ends in a closing tag and the loop stops.
//
// Reminders (skills, memory, one-time notices) re-emit fresh after compaction,
// so the summary should capture only real conversation turns.
func stripSystemReminders(content string) string {
	for {
		trimmed := strings.TrimRight(content, " \t\r\n")
		if !strings.HasSuffix(trimmed, systemReminderClose) {
			break
		}
		open := strings.LastIndex(trimmed, systemReminderOpen)
		if open < 0 {
			break
		}
		content = trimmed[:open]
	}
	return strings.TrimSpace(content)
}

// BuildConversationText converts messages to text, keeping harness
// <system-reminder> content intact. Use it where the real prompt size matters
// (e.g. conversation-growth estimation for proactive compaction), since the
// reminders are part of what actually gets sent to the model.
func BuildConversationText(msgs []Message) string {
	return buildConversationText(msgs, false)
}

// BuildCompactionText is like BuildConversationText but strips the trailing
// <system-reminder> blocks from each user message and drops messages that were
// nothing but reminders. Use it for summarization: reminders re-emit fresh
// after compaction, so the summary should capture only real conversation turns.
func BuildCompactionText(msgs []Message) string {
	return buildConversationText(msgs, true)
}

func buildConversationText(msgs []Message, stripReminders bool) string {
	var sb strings.Builder
	sb.WriteString("Please summarize this coding conversation:\n\n")

	for _, msg := range msgs {
		switch msg.Role {
		case RoleUser:
			if msg.ToolResult != nil {
				content := msg.ToolResult.Content
				if len(content) > 500 {
					content = content[:500] + "...[truncated]"
				}
				fmt.Fprintf(&sb, "[Tool Result: %s]\n%s\n\n", msg.ToolResult.ToolName, content)
			} else {
				content := msg.Content
				if stripReminders {
					content = stripSystemReminders(content)
					if content == "" {
						continue
					}
				}
				fmt.Fprintf(&sb, "User: %s\n\n", content)
			}

		case RoleAssistant:
			if msg.Content != "" {
				fmt.Fprintf(&sb, "Assistant: %s\n\n", msg.Content)
			}
			if len(msg.ToolCalls) > 0 {
				counts := make(map[string]int, len(msg.ToolCalls))
				order := make([]string, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					if counts[tc.Name] == 0 {
						order = append(order, tc.Name)
					}
					counts[tc.Name]++
				}
				parts := make([]string, 0, len(order))
				for _, name := range order {
					if counts[name] == 1 {
						parts = append(parts, name)
					} else {
						parts = append(parts, fmt.Sprintf("%s × %d", name, counts[name]))
					}
				}
				fmt.Fprintf(&sb, "[Tool Calls: %s]\n", strings.Join(parts, ", "))
				sb.WriteString("\n")
			}
		}
	}

	return sb.String()
}

// LastAssistantContent returns the most recent non-empty assistant content from provider messages.
func LastAssistantContent(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleAssistant && msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
}

// LastAssistantChatContent returns the most recent non-empty assistant content from chat messages.
func LastAssistantChatContent(msgs []ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleAssistant && msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
}

// NeedsCompaction checks if token usage exceeds the threshold percentage of the input limit.
func NeedsCompaction(inputTokens, inputLimit int) bool {
	if inputLimit == 0 || inputTokens == 0 {
		return false
	}
	return float64(inputTokens)/float64(inputLimit)*100 >= 95
}

// --- Content Parts ---

// ContentPartType distinguishes text from image in interleaved content.
type ContentPartType string

const (
	ContentPartText  ContentPartType = "text"
	ContentPartImage ContentPartType = "image"
)

// ContentPart represents a text or image segment in interleaved content.
type ContentPart struct {
	Type  ContentPartType
	Text  string
	Image *Image
}

var inlineImageTokenRe = regexp.MustCompile(`\[Image #(\d+)\]`)

// InterleavedContentParts parses [Image #N] tokens from display content and returns
// interleaved text and image parts.
func InterleavedContentParts(msg Message) []ContentPart {
	if len(msg.Images) == 0 || msg.DisplayContent == "" || !inlineImageTokenRe.MatchString(msg.DisplayContent) {
		return nil
	}

	idToIdx := BuildImageIDMap(msg.DisplayContent, len(msg.Images))

	var parts []ContentPart
	last := 0
	matches := inlineImageTokenRe.FindAllStringSubmatchIndex(msg.DisplayContent, -1)
	if len(matches) > 0 {
		parts = make([]ContentPart, 0, len(matches)*2+1)
	}
	for _, match := range matches {
		start, end := match[0], match[1]
		idStart, idEnd := match[2], match[3]

		if text := msg.DisplayContent[last:start]; text != "" {
			parts = append(parts, ContentPart{Type: ContentPartText, Text: text})
		}

		id, err := strconv.Atoi(msg.DisplayContent[idStart:idEnd])
		if err == nil {
			if idx, ok := idToIdx[id]; ok && idx < len(msg.Images) {
				img := msg.Images[idx]
				parts = append(parts, ContentPart{Type: ContentPartImage, Image: &img})
			}
		}

		last = end
	}

	if tail := msg.DisplayContent[last:]; tail != "" {
		parts = append(parts, ContentPart{Type: ContentPartText, Text: tail})
	}

	if len(parts) == 0 {
		return nil
	}
	return parts
}

// BuildImageIDMap parses [Image #N] tokens from displayContent and returns a map
// from token ID to sequential index (0-based). imageCount caps the number of entries.
func BuildImageIDMap(displayContent string, imageCount int) map[int]int {
	m := make(map[int]int, imageCount)
	matches := inlineImageTokenRe.FindAllStringSubmatch(displayContent, -1)
	idx := 0
	for _, match := range matches {
		id, err := strconv.Atoi(match[1])
		if err == nil && idx < imageCount {
			m[id] = idx
			idx++
		}
	}
	return m
}
