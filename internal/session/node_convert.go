package session

import (
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session/transcript"
)

// messagesToNodes projects the wire messages onto transcript nodes for the
// append-only save path. Node content comes from the shared MessageToBlocks
// converter — the same one the live Recorder writes through — so both writers
// produce byte-identical content for a given message ID. Only user/assistant
// messages become nodes (control signals are not model-visible); each node's
// timestamp is derived from createdAt so a re-save is deterministic.
//
// Rows that share an ID are the results of one parallel-call turn (see
// core.ChatRowsOf); they fold back into that turn's single node, the inverse
// of the split messagesFromNodes makes.
func messagesToNodes(msgs []core.ChatMessage, defaultCwd string, createdAt time.Time, gitBranch string) []transcript.Node {
	nodes := make([]transcript.Node, 0, len(msgs))
	var prevID string

	for _, msg := range msgs {
		role := transcriptRole(msg.Role)
		if role == "" {
			continue
		}
		id := msg.ID
		if id == "" {
			id = core.NewMessageID()
		}
		if id == prevID && msg.ToolResult != nil {
			last := &nodes[len(nodes)-1]
			last.Content = append(last.Content, MessageToBlocks(msg)...)
			continue
		}
		nodes = append(nodes, transcript.Node{
			ID:        id,
			ParentID:  prevID,
			Role:      role,
			Time:      createdAt.Add(time.Duration(len(nodes)+1) * time.Millisecond),
			Cwd:       defaultCwd,
			GitBranch: gitBranch,
			Content:   MessageToBlocks(msg),
		})
		prevID = id
	}

	return nodes
}

// messagesFromNodes rebuilds the wire messages from transcript nodes on load.
// tool_result blocks carry only a tool_use id, so a first pass indexes tool
// names from assistant tool_use blocks to backfill ToolResult.ToolName.
func messagesFromNodes(nodes []transcript.Node) []core.ChatMessage {
	toolNameByID := make(map[string]string)
	for _, node := range nodes {
		if node.Role == "assistant" {
			for _, block := range node.Content {
				if block.Type == "tool_use" {
					toolNameByID[block.ID] = block.Name
				}
			}
		}
	}

	msgs := make([]core.ChatMessage, 0, len(nodes))
	for _, node := range nodes {
		if node.Role == "assistant" {
			msg := core.ChatMessage{Role: core.ChatAssistant, ID: node.ID}
			extractAssistantContent(node.Content, &msg)
			msgs = append(msgs, msg)
			continue
		}
		// A parallel-call turn is one node carrying a tool_result block per
		// call; the conversation shows each result as its own row.
		for _, blocks := range splitToolResults(node.Content) {
			msg := core.ChatMessage{Role: core.ChatUser, ID: node.ID}
			extractUserContent(blocks, &msg)
			if msg.ToolResult != nil && msg.ToolResult.ToolName == "" {
				if name, ok := toolNameByID[msg.ToolResult.ToolCallID]; ok {
					msg.ToolResult.ToolName = name
				}
			}
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

// splitToolResults returns the node content as one row's worth of blocks per
// tool_result, or the content whole when it carries at most one.
func splitToolResults(content []ContentBlock) [][]ContentBlock {
	var rows [][]ContentBlock
	for _, block := range content {
		if block.Type == "tool_result" {
			rows = append(rows, []ContentBlock{block})
		}
	}
	if len(rows) <= 1 {
		return [][]ContentBlock{content}
	}
	return rows
}

// transcriptRole maps a wire role onto the transcript's role string. Only
// user and assistant turns are persisted; anything else returns "" so the
// caller skips it.
func transcriptRole(role core.ChatRole) string {
	switch role {
	case core.ChatUser:
		return "user"
	case core.ChatAssistant:
		return "assistant"
	default:
		return ""
	}
}
