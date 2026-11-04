package app

import (
	"context"
	"encoding/json"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/interaction"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
)

// Permissions are deliberately once-only. No transport response can change
// native deny/ask precedence or install a persistent grant.
func (s *acpSession) permissions(ctx context.Context, turn *acp.Turn, gate *agent.PermissionGate, done chan<- struct{}) {
	defer close(done)
	for {
		req, ok := gate.RecvContext(ctx)
		if !ok {
			return
		}
		if req.Context.Err() != nil {
			continue
		}
		// Hook-forced prompts have not visited the rules decider. A forced
		// question must not let the client waive a native deny or host limit.
		if req.RequestID == "" {
			decision := s.params.PermissionRules(req.ToolName, req.Input)
			if decision.Decision == perm.Reject {
				select {
				case req.Response <- agent.PermGateResponse{Reason: decision.Reason}:
				case <-req.Context.Done():
				}
				continue
			}
			req.RequestID = core.NewMessageID()
		}
		input, _ := json.Marshal(req.Input)
		record := transcript.PermissionRecord{RequestID: req.RequestID, Tool: req.ToolName, Input: input,
			Source: transcript.PermissionSourceAsk, OptionsOffered: []string{"allow_once", "reject_once"}}
		_ = s.data.Session.Append(context.Background(), transcript.Record{Type: transcript.PermissionRequired, Permission: &record})
		response, err := turn.RequestPermission(protocol.RequestPermissionRequest{
			ToolCall: protocol.ToolCallUpdate{ToolCallID: protocol.ToolCallID(req.ToolCallID), Title: protocol.Some(req.ToolName), RawInput: protocol.Some(json.RawMessage(input))},
			Options: []protocol.PermissionOption{
				{OptionID: "allow_once", Kind: "allow_once", Name: "Allow once"},
				{OptionID: "reject_once", Kind: "reject_once", Name: "Reject once"},
			},
		})
		selected, ok := response.Outcome.Value.(protocol.RequestPermissionOutcomeSelectedVariant)
		allow := err == nil && ok && selected.OptionID == "allow_once" && req.Context.Err() == nil
		reason := "user denied"
		if allow {
			reason = "user approved"
		}
		if err != nil || req.Context.Err() != nil {
			reason = "interaction cancelled"
		}
		record.Source, record.Scope, record.Reason = transcript.PermissionSourceUser, "once", reason
		record.Decision = interaction.PermissionDecisionFor(allow)
		_ = s.data.Session.Append(context.Background(), transcript.Record{Type: transcript.PermissionDecided, Permission: &record})
		select {
		case req.Response <- agent.PermGateResponse{Allow: allow, Reason: reason}:
		case <-req.Context.Done():
		}
	}
}
