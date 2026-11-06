package app

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
)

func (s *acpSession) modes() protocol.SessionModeState {
	return protocol.SessionModeState{CurrentModeID: protocol.SessionModeID(s.permissions.CurrentMode().PersistenceName()), AvailableModes: []protocol.SessionMode{
		{ID: "normal", Name: "Normal", Description: protocol.Some("Native permission rules and interactive approvals")},
		{ID: "auto-accept", Name: "Auto-accept", Description: protocol.Some("Native auto-accept rules; explicit deny and ask rules still apply")},
	}}
}

func (h *acpHost) setMode(ctx context.Context, req protocol.SetSessionModeRequest) (protocol.SetSessionModeResponse, error) {
	h.mu.Lock()
	s := h.sessions[req.SessionID]
	h.mu.Unlock()
	if s == nil {
		return protocol.SetSessionModeResponse{}, fmt.Errorf("unknown session")
	}
	if err := s.requireConfigIdle(); err != nil {
		return protocol.SetSessionModeResponse{}, err
	}
	var mode setting.OperationMode
	switch req.ModeID {
	case "normal":
		mode = setting.ModeNormal
	case "auto-accept":
		mode = setting.ModeAutoAccept
	default:
		return protocol.SetSessionModeResponse{}, &acp.Error{Code: -32602, Message: "unsupported mode: only normal and auto-accept are available; bypass and autopilot are not supported"}
	}
	if err := ctx.Err(); err != nil {
		return protocol.SetSessionModeResponse{}, err
	}
	// The endpoint admits this only while idle. Persist before changing the permission mode.
	if err := s.nativeSession.Session.Append(ctx, transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StatePatch{Mode: new(mode.PersistenceName())}}); err != nil {
		return protocol.SetSessionModeResponse{}, err
	}
	s.permissions.SetMode(mode)
	return protocol.SetSessionModeResponse{}, nil
}

func (h *acpHost) load(ctx context.Context, turn *acp.Turn, req protocol.LoadSessionRequest) (response protocol.LoadSessionResponse, err error) {
	created, err := h.open(ctx, protocol.NewSessionRequest{CWD: req.CWD, MCPServers: req.MCPServers}, string(req.SessionID))
	if err != nil {
		return response, err
	}
	h.mu.Lock()
	s := h.sessions[created.SessionID]
	h.mu.Unlock()
	defer func() {
		if err != nil {
			h.mu.Lock()
			delete(h.sessions, created.SessionID)
			h.mu.Unlock()
			err = errors.Join(err, s.close())
		}
	}()
	rows, err := s.nativeSession.Store.ReadChatHistory(string(req.SessionID))
	if err != nil {
		return response, err
	}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		if row.ToolResult != nil {
			status := protocol.ToolCallStatusCompleted
			if row.ToolResult.IsError {
				status = protocol.ToolCallStatusFailed
			}
			if err = turn.SendUpdate(protocol.SessionNotification{Update: acpToolUpdate(row.ToolResult.ToolCallID, row.ToolResult.Content.Text(), status)}); err != nil {
				return response, err
			}
			continue
		}
		text := row.Content
		if row.Role == core.ChatUser {
			text = row.DisplayContent
		}
		for len(text) > 0 {
			n := min(len(text), 32<<10)
			for n < len(text) && !utf8.RuneStart(text[n]) {
				n--
			}
			var update protocol.SessionUpdate
			if row.Role == core.ChatUser {
				update.Value = protocol.SessionUpdateUserMessageChunkVariant{SessionUpdate: "user_message_chunk", Content: acpText(text[:n])}
			} else {
				update.Value = protocol.SessionUpdateAgentMessageChunkVariant{SessionUpdate: "agent_message_chunk", Content: acpText(text[:n])}
			}
			if err = turn.SendUpdate(protocol.SessionNotification{Update: update}); err != nil {
				return response, err
			}
			text = text[n:]
		}
		for _, call := range row.ToolCalls {
			update := protocol.SessionUpdate{Value: protocol.SessionUpdateToolCallVariant{SessionUpdate: "tool_call", ToolCallID: protocol.ToolCallID(call.ID), Title: call.Name, Status: protocol.Some(protocol.ToolCallStatusCompleted)}}
			if err = turn.SendUpdate(protocol.SessionNotification{Update: update}); err != nil {
				return response, err
			}
		}
	}
	if err := s.sendCommands(turn); err != nil {
		return response, err
	}
	return protocol.LoadSessionResponse{Modes: created.Modes, ConfigOptions: created.ConfigOptions}, s.nativeSession.Session.Err()
}
