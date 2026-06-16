package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/genai-io/san/internal/core"
)

// systemReminderRe matches a complete <system-reminder>...</system-reminder>
// block including tags. Reminders are appended verbatim by reminder.AttachToContent
// and hook UserPromptSubmit additionalContext flows through the same path, so
// recognizing the wrapper suffices to mark harness-injected content.
//
// Captures the optional source="..." attribute so the transcript can attribute
// "which provider injected this reminder" (skills-directory, memory-user,
// hook context, etc.) without parsing the body.
var systemReminderRe = regexp.MustCompile(`(?s)<system-reminder(?:\s+source="([^"]*)")?>.*?</system-reminder>`)

const SourceReminder = "reminder"

// splitTextByProvenance returns ContentBlocks that together preserve the input
// byte-for-byte, marking <system-reminder> blocks with Source="reminder" so
// traces can distinguish user-typed text from harness-injected reminders /
// hook additionalContext. Empty input returns nil.
//
// The function never trims whitespace — concatenating all returned Text fields
// in order reproduces the original input. This keeps the read path
// (extractUserContent, which joins text blocks) round-trip safe.
func splitTextByProvenance(text string) []ContentBlock {
	if text == "" {
		return nil
	}
	matches := systemReminderRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return []ContentBlock{{Type: "text", Text: text}}
	}

	blocks := make([]ContentBlock, 0, 2*len(matches)+1)
	cursor := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if start > cursor {
			blocks = append(blocks, ContentBlock{Type: "text", Text: text[cursor:start]})
		}
		source := SourceReminder
		// m[2]/m[3] are the source-capture group; -1 means absent.
		if len(m) >= 4 && m[2] >= 0 && m[3] >= 0 && m[3] > m[2] {
			source = SourceReminder + ":" + text[m[2]:m[3]]
		}
		blocks = append(blocks, ContentBlock{Type: "text", Text: text[start:end], Source: source})
		cursor = end
	}
	if cursor < len(text) {
		blocks = append(blocks, ContentBlock{Type: "text", Text: text[cursor:]})
	}
	return blocks
}

func messagesToEntries(msgs []core.Message) []Entry {
	entries := make([]Entry, 0, len(msgs))
	var prevUUID string

	for _, msg := range msgs {
		// Prefer the stable ID stamped upstream (conv.Append, agent.append) so
		// the append-only save path can dedupe by it; fall back only for
		// messages assembled without one.
		uuid := msg.ID
		if uuid == "" {
			uuid = core.NewMessageID()
		}

		var parentUuid *string
		if prevUUID != "" {
			s := prevUUID
			parentUuid = &s
		}

		entry := Entry{
			UUID:       uuid,
			ParentUuid: parentUuid,
			Version:    GetAppVersion(),
		}

		switch msg.Role {
		case core.RoleUser:
			entry.Type = EntryUser
			if msg.ToolResult != nil {
				entry.Message = &EntryMessage{Role: "user", Content: toolResultToBlocks(msg.ToolResult)}
			} else {
				entry.Message = &EntryMessage{Role: "user", Content: userContentToBlocks(msg.Content, msg.DisplayContent, msg.Images)}
			}
		case core.RoleAssistant:
			entry.Type = EntryAssistant
			entry.Message = &EntryMessage{
				Role:    "assistant",
				Content: assistantContentToBlocks(msg.Content, msg.Thinking, msg.ThinkingSignature, msg.ToolCalls),
			}
		default:
			continue
		}

		entries = append(entries, entry)
		prevUUID = uuid
	}

	return entries
}

func EntriesToMessages(entries []Entry) []core.Message {
	toolNameMap := make(map[string]string)
	for _, entry := range entries {
		if entry.Type == EntryAssistant && entry.Message != nil {
			for _, block := range entry.Message.Content {
				if block.Type == "tool_use" {
					toolNameMap[block.ID] = block.Name
				}
			}
		}
	}

	msgs := make([]core.Message, 0, len(entries))
	for _, entry := range entries {
		switch entry.Type {
		case EntryUser:
			msg := core.Message{Role: core.RoleUser, ID: entry.UUID}
			if entry.Message != nil {
				extractUserContent(entry.Message.Content, &msg)
			}
			if msg.ToolResult != nil && msg.ToolResult.ToolName == "" {
				if name, ok := toolNameMap[msg.ToolResult.ToolCallID]; ok {
					msg.ToolResult.ToolName = name
				}
			}
			msgs = append(msgs, msg)
		case EntryAssistant:
			msg := core.Message{Role: core.RoleAssistant, ID: entry.UUID}
			if entry.Message != nil {
				extractAssistantContent(entry.Message.Content, &msg)
			}
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

func userContentToBlocks(content, displayContent string, images []core.Image) []ContentBlock {
	if len(images) > 0 && displayContent != "" && core.InlineImageTokenRe.MatchString(displayContent) {
		return interleavedUserContentToBlocks(content, displayContent, images)
	}

	var blocks []ContentBlock
	for _, img := range images {
		blocks = append(blocks, ContentBlock{
			Type:        "image",
			ImageSource: &ImageSource{Type: "base64", MediaType: img.MediaType, Data: img.Data},
		})
	}
	blocks = append(blocks, splitTextByProvenance(content)...)
	return blocks
}

func interleavedUserContentToBlocks(content, displayContent string, images []core.Image) []ContentBlock {
	var blocks []ContentBlock
	last := 0

	idToIdx := core.BuildImageIDMap(displayContent, len(images))

	matches := core.InlineImageTokenRe.FindAllStringSubmatchIndex(displayContent, -1)
	for _, match := range matches {
		start, end := match[0], match[1]
		idStart, idEnd := match[2], match[3]

		if textPart := displayContent[last:start]; textPart != "" {
			blocks = append(blocks, splitTextByProvenance(textPart)...)
		}

		id, err := strconv.Atoi(displayContent[idStart:idEnd])
		if err == nil {
			if idx, ok := idToIdx[id]; ok && idx < len(images) {
				img := images[idx]
				blocks = append(blocks, ContentBlock{
					Type:        "image",
					ImageSource: &ImageSource{Type: "base64", MediaType: img.MediaType, Data: img.Data},
				})
			}
		}

		last = end
	}

	if tail := displayContent[last:]; tail != "" {
		blocks = append(blocks, splitTextByProvenance(tail)...)
	}

	if len(blocks) == 0 && content != "" {
		blocks = append(blocks, splitTextByProvenance(content)...)
	}

	return blocks
}

func assistantContentToBlocks(content, thinking, thinkingSignature string, toolCalls []core.ToolCall) []ContentBlock {
	var blocks []ContentBlock
	if thinking != "" {
		blocks = append(blocks, ContentBlock{Type: "thinking", Thinking: thinking, Signature: thinkingSignature})
	}
	if content != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: content})
	}
	for _, tc := range toolCalls {
		block := ContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name}
		if tc.Input != "" {
			block.Input = json.RawMessage(tc.Input)
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func toolResultToBlocks(tr *core.ToolResult) []ContentBlock {
	block := ContentBlock{Type: "tool_result", ToolUseID: tr.ToolCallID, IsError: tr.IsError}
	if tr.Content != "" {
		block.Content = []ContentBlock{{Type: "text", Text: tr.Content}}
	}
	return []ContentBlock{block}
}

func ExtractLastUserText(entries []Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if text, ok := extractUserText(entries[i]); ok {
			return text
		}
	}
	return ""
}

func extractUserContent(blocks []ContentBlock, msg *core.Message) {
	imageCount := 0
	var display strings.Builder
	var content strings.Builder

	for _, block := range blocks {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
			// <system-reminder> blocks are harness-injected context the model
			// sees but the user must not: keep them in Content, exclude them
			// from the displayed text (otherwise a resumed session re-renders
			// the whole reminder block to the user).
			if !strings.HasPrefix(block.Source, SourceReminder) {
				display.WriteString(block.Text)
			}
		case "image":
			if block.ImageSource != nil {
				msg.Images = append(msg.Images, core.Image{MediaType: block.ImageSource.MediaType, Data: block.ImageSource.Data})
				imageCount++
				display.WriteString(fmt.Sprintf("[Image #%d]", imageCount))
			}
		case "tool_result":
			tr := &core.ToolResult{ToolCallID: block.ToolUseID, IsError: block.IsError}
			for _, sub := range block.Content {
				if sub.Type == "text" {
					tr.Content = sub.Text
				}
			}
			msg.ToolResult = tr
		}
	}

	if msg.ToolResult == nil {
		msg.Content = content.String()
		// Trim the trailing blank line left where a reminder was appended
		// (AttachToContent joins with "\n\n"), so the displayed text matches
		// what the user originally typed.
		msg.DisplayContent = strings.TrimRight(display.String(), "\n")
	}
}

func extractAssistantContent(blocks []ContentBlock, msg *core.Message) {
	var content strings.Builder
	for _, block := range blocks {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "thinking":
			msg.Thinking = block.Thinking
			msg.ThinkingSignature = block.Signature
		case "tool_use":
			tc := core.ToolCall{ID: block.ID, Name: block.Name}
			if block.Input != nil {
				tc.Input = string(block.Input)
			}
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	msg.Content = content.String()
}

func extractUserText(entry Entry) (string, bool) {
	if entry.Type != EntryUser || entry.Message == nil {
		return "", false
	}
	var text string
	for _, block := range entry.Message.Content {
		if block.Type == "tool_result" {
			return "", false
		}
		if block.Type == "text" && block.Text != "" && text == "" {
			text = block.Text
		}
	}
	if text != "" {
		return text, true
	}
	return "", false
}
