package interaction

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// PreparePermission validates the call and derives the native preview and grant eligibility.
func PreparePermission(req *agent.PermGateRequest, cwd, mode string, settings *setting.Settings, grants *setting.SessionPermissions) (*perm.PermissionRequest, error) {
	view := &perm.PermissionRequest{ToolName: req.ToolName, Description: req.Description}
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if resolved, ok := tool.Get(req.ToolName); ok {
		if aware, ok := resolved.(tool.PermissionAwareTool); ok {
			rich, err := aware.PreparePermission(ctx, req.Input, cwd)
			if err != nil {
				return nil, err
			}
			if rich != nil {
				view = rich
			}
		}
	}
	view.ID = req.RequestID
	view.WorkingDirectory = cwd
	view.PermissionMode = mode
	view.AllowRules = settings.AlwaysAllowRules(req.ToolName, req.Input, grants)
	view.OnceOnly = view.OnceOnly || !settings.SessionGrantTakesEffect(req.ToolName, req.Input, grants)
	return view, nil
}

// PermissionDetail records derived preview context without duplicating raw arguments.
func PermissionDetail(req *Request, view *perm.PermissionRequest) json.RawMessage {
	detail := permissionDetail(view)
	if req.Source.AgentID == "" {
		return detail
	}
	data, _ := json.Marshal(struct {
		Agent      tool.AgentInteractionSource `json:"agent"`
		Permission json.RawMessage             `json:"permission,omitempty"`
	}{req.Source, detail})
	return data
}

func permissionDetail(req *perm.PermissionRequest) json.RawMessage {
	if req == nil {
		return nil
	}
	var payload any
	switch {
	case req.SkillMeta != nil:
		m := req.SkillMeta
		payload = struct {
			Description string   `json:"description,omitempty"`
			ScriptCount int      `json:"scriptCount,omitempty"`
			RefCount    int      `json:"refCount,omitempty"`
			Scripts     []string `json:"scripts,omitempty"`
			References  []string `json:"references,omitempty"`
		}{m.Description, m.ScriptCount, m.RefCount, m.Scripts, m.References}
	case req.BashMeta != nil:
		payload = struct {
			LineCount int `json:"lineCount,omitempty"`
		}{req.BashMeta.LineCount}
	case req.AgentMeta != nil:
		payload = req.AgentMeta
	case req.DiffMeta != nil:
		payload = req.DiffMeta
	default:
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Logger().Warn("perm detail marshal failed", zap.Error(err))
		return nil
	}
	return data
}
