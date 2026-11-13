package subagent_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/interaction"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

type integrationApprovalResolver struct {
	bridge *conv.AgentToUI
	source tool.AgentInteractionSource
}

func (r integrationApprovalResolver) Resolve(ctx context.Context, name string, input map[string]any, _ bool, _ string) perm.Resolution {
	view := &perm.PermissionRequest{ToolName: name, Description: "Execute " + name}
	if registered, ok := tool.Get(name); ok {
		if aware, ok := registered.(tool.PermissionAwareTool); ok {
			prepared, err := aware.PreparePermission(ctx, input, r.source.WorkingDirectory)
			if err != nil {
				return perm.Resolution{Decision: "deny", By: "config", Reason: err.Error()}
			}
			if prepared != nil {
				view = prepared
			}
		}
	}
	view.ID, view.ToolName = tool.GenerateRequestID(), name
	view.CallerAgent, view.WorkingDirectory = r.source.Label(), r.source.WorkingDirectory
	view.PermissionMode, view.OnceOnly, view.AllowRules = r.source.PermissionMode, true, nil
	return r.bridge.ResolveApproval(ctx, r.source, view, input)
}

// The factory receives real executor-owned invocation metadata. Human decisions
// cross the native UI bridge, so audit authority and displayed questions survive.
func configureIntegrationApprovals(t *testing.T, executor *subagent.Executor, decide func(context.Context, tool.AgentInteractionSource, *perm.PermissionRequest, map[string]any) interaction.PermissionDecision) {
	t.Helper()
	bridge := conv.NewAgentToUI(8)
	ctx, cancel := context.WithCancel(t.Context())
	var requests sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			msg, ok := bridge.Check()().(conv.InteractionRequestMsg)
			if !ok {
				time.Sleep(time.Millisecond)
				continue
			}
			requests.Add(1)
			go func(req *conv.InteractionRequest) {
				defer requests.Done()
				displayed := interaction.NewPermission(req, bridge, req.PermissionView, func(*perm.PermissionRequest) []string { return []string{"Allow once", "Deny"} })
				if displayed == nil {
					t.Error("native approval was not displayed")
					return
				}
				decision := decide(req.Context, req.Source, req.PermissionView, req.Permission.Input)
				if req.Context.Err() != nil {
					return
				}
				if !displayed.Resolve(bridge, decision, nil, nil) {
					t.Error("native approval reply rejected")
				}
			}(msg.Request)
		}
	}()
	t.Cleanup(func() { cancel(); bridge.Close(); <-done; requests.Wait() })
	executor.ConfigureOwner(nil, nil, func(_ *session.Session, _ bool, source tool.AgentInteractionSource) tool.Resolver {
		return integrationApprovalResolver{bridge: bridge, source: source}
	}, nil)
}
