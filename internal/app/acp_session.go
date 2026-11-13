package app

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/toolresult"
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
	if err := session.NewRecorder(s.nativeSession.Session, s.runtimeBuildParams.Provider.Name()).RecordState(ctx, session.SessionUpdatePayload{Config: &session.ConfigPatch{Mode: session.SetField(mode.PersistenceName())}}); err != nil {
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
	history, err := s.nativeSession.Store.Read(ctx, string(req.SessionID))
	if err != nil {
		return response, err
	}
	unfinished := history.Unfinished()
	if len(unfinished.Inferences)+len(unfinished.NeverRan)+len(unfinished.Unknown) > 0 {
		text := fmt.Sprintf("Interrupted session: unfinished model requests %v; tools that never ran %v; tools with unknown outcome %v. Interrupted work has not been retried.", unfinished.Inferences, unfinished.NeverRan, unfinished.Unknown)
		if err = turn.SendUpdate(protocol.SessionNotification{Update: protocol.SessionUpdate{Value: protocol.SessionUpdateAgentMessageChunkVariant{SessionUpdate: "agent_message_chunk", Content: acpText(text)}}}); err != nil {
			return response, err
		}
	}
	finished := make(map[string]session.EventOutcome)
	for _, event := range history.Events() {
		if result, ok := event.Payload.(*session.ToolFinishPayload); ok {
			finished[result.CallID] = result.Outcome
		}
	}
	rows := s.nativeSession.Store.ChatRows(history)
	titles := make(map[string]string)
	for _, row := range rows {
		if row.ToolResult != nil {
			if details, ok := row.ToolDetails.(toolresult.BashDetails); ok && details.ShellLabel != "" {
				titles[row.ToolResult.ToolCallID] = details.ShellLabel
			}
		}
	}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return response, err
		}
		if row.ToolResult != nil {
			status := protocol.ToolCallStatusInProgress
			if outcome, ok := finished[row.ToolResult.ToolCallID]; ok && outcome == session.OutcomeSuccess {
				status = protocol.ToolCallStatusCompleted
			}
			if row.ToolResult.IsError || finished[row.ToolResult.ToolCallID] == session.OutcomeFailure || finished[row.ToolResult.ToolCallID] == session.OutcomeCancelled {
				status = protocol.ToolCallStatusFailed
			}
			if err = turn.SendUpdate(protocol.SessionNotification{Update: acpToolUpdate(row.ToolResult.ToolCallID, row.ToolResult.ToolName, row.ToolResult.Content.Text(), status, row.ToolDetails)}); err != nil {
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
			title := call.Name
			if label := titles[call.ID]; label != "" {
				title = label
			}
			update := protocol.SessionUpdate{Value: protocol.SessionUpdateToolCallVariant{SessionUpdate: "tool_call", ToolCallID: protocol.ToolCallID(call.ID), Title: title, Status: protocol.Some(protocol.ToolCallStatusInProgress)}}
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
