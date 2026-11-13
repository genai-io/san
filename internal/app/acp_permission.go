package app

import (
	"context"
	"encoding/json"

	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/interaction"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
)

// Permissions are deliberately once-only. No transport response can change
// native deny/ask precedence or install a persistent grant.
func (s *acpSession) handlePermissionRequests(ctx context.Context, turn *acp.Turn, gate *agent.PermissionGate, done chan<- struct{}) {
	defer close(done)
	for {
		req, ok := gate.WaitRequest(ctx)
		if !ok {
			return
		}
		if req.Context.Err() != nil {
			continue
		}
		// Hook-forced prompts have not visited the rules decider. A forced
		// question must not let the client waive a native deny or host limit.
		if req.RequestID == "" {
			decision := s.runtimeBuildParams.PermissionRules(req.ToolName, req.Input)
			if decision.Decision == perm.Reject {
				select {
				case req.Response <- agent.PermGateResponse{Reason: decision.Reason, By: decision.By, Scope: decision.Scope}:
				case <-req.Context.Done():
				}
				continue
			}
			req.RequestID = core.NewMessageID()
		}
		title := req.ToolName
		if tool.IsShellTool(req.ToolName) {
			view, prepareErr := interaction.PreparePermission(req, s.runtimeBuildParams.CWD, s.permissions.CurrentMode().PersistenceName(), setting.New(s.permissions.settings), s.permissions.SessionPermissions)
			if prepareErr != nil {
				select {
				case req.Response <- agent.PermGateResponse{Reason: prepareErr.Error()}:
				case <-req.Context.Done():
				}
				continue
			}
			if view.BashMeta != nil && view.BashMeta.ShellLabel != "" {
				title = view.BashMeta.ShellLabel
			}
		}
		input, _ := json.Marshal(req.Input)
		asked := perm.Asked{Options: []string{"allow_once", "reject_once"}, Detail: input, At: time.Now().UnixMilli()}
		req.MarkAsked(asked)
		response, err := turn.RequestPermission(protocol.RequestPermissionRequest{
			ToolCall: protocol.ToolCallUpdate{ToolCallID: protocol.ToolCallID(req.ToolCallID), Title: protocol.Some(title), RawInput: protocol.Some(json.RawMessage(input))},
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
		if ctx.Err() != nil {
			// Fence the native actor before releasing a cancelled foreground
			// approval; the prompt event consumer may still be unwinding I/O.
			s.runtime.Cancel()
		}
		select {
		case req.Response <- agent.PermGateResponse{Allow: allow, Reason: reason, By: "user", Scope: "once", Asked: &asked, Cancelled: err != nil || req.Context.Err() != nil}:
		case <-req.Context.Done():
		}
	}
}
