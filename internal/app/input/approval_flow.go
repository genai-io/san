package input

import (
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	gozap "go.uber.org/zap"

	"github.com/genai-io/san/v2/internal/app/conv"
	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/hook"
	"github.com/genai-io/san/v2/internal/log"
	"github.com/genai-io/san/v2/internal/setting"
	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/perm"
)

type HookPermissionResultMsg struct {
	Request *perm.PermissionRequest
	Blocked bool
	Allowed bool
	Reason  string
	Outcome hook.HookOutcome
}

// ApprovalRuntime provides app-level operations that the approval flow needs.
type ApprovalRuntime interface {
	AbortToolWithError(msg string, retry bool) tea.Cmd
	ReloadProjectContext(cwd string)
}

type ApprovalFlowDeps struct {
	Actions            ApprovalRuntime
	Input              *Model
	HookEngine         hook.Handler
	Settings           *setting.Data
	SessionPermissions *setting.SessionPermissions
	SetOperationMode   func(setting.OperationMode)
	Tool               *conv.ToolExecState
	Width              int
	Height             int
	Cwd                string
	ProgressHub        *conv.ProgressHub
	MCPExecutor        tool.MCPExecutor
}

func UpdateApproval(deps ApprovalFlowDeps, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case ApprovalRequestMsg:
		return HandlePermissionRequest(deps, msg), true
	case HookPermissionResultMsg:
		return HandleHookPermissionResult(deps, msg), true
	default:
		return nil, false
	}
}

func HandlePermissionRequest(deps ApprovalFlowDeps, msg ApprovalRequestMsg) tea.Cmd {
	if deps.HookEngine != nil && deps.HookEngine.HasHooks(hook.PermissionRequest) && msg.Request != nil {
		return tea.Batch(
			ShowApprovalModal(deps, msg.Request),
			DispatchPermissionHookAsync(deps, msg.Request),
		)
	}
	return ShowApprovalModal(deps, msg.Request)
}

func DispatchPermissionHookAsync(deps ApprovalFlowDeps, req *perm.PermissionRequest) tea.Cmd {
	hookEngine := deps.HookEngine
	ctx := deps.Tool.Context()
	hookInput := hook.HookInput{ToolName: req.ToolName, ToolInput: fullToolInputForHook(deps, req)}
	hookInput.PermissionSuggestions = buildPermissionSuggestions(req)
	return func() tea.Msg {
		outcome := hookEngine.Execute(ctx, hook.PermissionRequest, hookInput)
		return HookPermissionResultMsg{
			Request: req,
			Blocked: outcome.ShouldBlock,
			Allowed: outcome.PermissionAllow,
			Reason:  outcome.BlockReason,
			Outcome: outcome,
		}
	}
}

func HandleHookPermissionResult(deps ApprovalFlowDeps, msg HookPermissionResultMsg) tea.Cmd {
	if !isCurrentPermissionRequest(deps, msg.Request) {
		return nil
	}
	if msg.Blocked {
		deps.Input.Approval.Hide()
		return deps.Actions.AbortToolWithError("Blocked by hook: "+msg.Reason, false)
	}
	if msg.Allowed {
		applyPermissionUpdates(deps, msg.Outcome.UpdatedPermissions)
		if msg.Outcome.UpdatedInput != nil {
			ApplyUpdatedToolInput(deps.Tool, msg.Outcome.UpdatedInput)
		}
		args := buildPermissionArgs(msg.Request)
		if deps.Settings != nil && deps.Settings.ResolveHookAllow(msg.Request.ToolName, args, deps.SessionPermissions) {
			deps.Input.Approval.Hide()
			return conv.ExecuteApproved(deps.Tool.Context(), deps.ProgressHub, deps.Tool.PendingCalls, deps.Tool.CurrentIdx, deps.Cwd, deps.MCPExecutor)
		}
	}
	return ShowApprovalModal(deps, msg.Request)
}

func ShowApprovalModal(deps ApprovalFlowDeps, req *perm.PermissionRequest) tea.Cmd {
	if req != nil {
		req.SuggestedRules = setting.GenerateSuggestions(req.ToolName, buildPermissionArgs(req), 5)
	}
	deps.Input.Approval.Show(req, deps.Width, deps.Height)
	if deps.HookEngine != nil {
		deps.HookEngine.ExecuteAsync(hook.Notification, hook.HookInput{
			Message:          "Permission required for " + req.ToolName,
			NotificationType: "permission_prompt",
		})
	}
	return nil
}

func isCurrentPermissionRequest(deps ApprovalFlowDeps, req *perm.PermissionRequest) bool {
	if req == nil || !deps.Input.Approval.IsActive() {
		return false
	}
	current := deps.Input.Approval.GetRequest()
	if current == nil {
		return false
	}
	return current.ID != "" && current.ID == req.ID
}

func buildPermissionSuggestions(req *perm.PermissionRequest) []hook.PermissionSuggestion {
	var suggestions []hook.PermissionSuggestion
	if req.FilePath != "" {
		dir := req.FilePath
		if i := strings.LastIndex(dir, "/"); i > 0 {
			dir = dir[:i]
		}
		suggestions = append(suggestions, hook.PermissionSuggestion{Type: "addDirectories", Directories: []string{dir}, Destination: "session"})
	}
	if req.ToolName == "Edit" || req.ToolName == "Write" {
		suggestions = append(suggestions, hook.PermissionSuggestion{Type: "setMode", Mode: "acceptEdits", Destination: "session"})
	}
	return suggestions
}

func buildPermissionArgs(req *perm.PermissionRequest) map[string]any {
	args := make(map[string]any)
	if req == nil {
		return args
	}
	if req.FilePath != "" {
		args["file_path"] = req.FilePath
	}
	if req.BashMeta != nil {
		args["command"] = req.BashMeta.Command
	}
	if req.SkillMeta != nil {
		args["skill"] = req.SkillMeta.SkillName
	}
	return args
}

func fullToolInputForHook(deps ApprovalFlowDeps, req *perm.PermissionRequest) map[string]any {
	if deps.Tool.PendingCalls != nil && deps.Tool.CurrentIdx < len(deps.Tool.PendingCalls) {
		tc := deps.Tool.PendingCalls[deps.Tool.CurrentIdx]
		if tc.Name == req.ToolName {
			if params, err := core.ParseToolInput(tc.Input); err == nil {
				return params
			}
		}
	}
	return buildPermissionArgs(req)
}

func applyPermissionUpdates(deps ApprovalFlowDeps, updates []hook.PermissionUpdate) {
	needReload := false
	for _, pu := range updates {
		switch pu.Type {
		case "setMode":
			if deps.SessionPermissions != nil {
				switch pu.Mode {
				case "bypassPermissions":
					log.Logger().Warn("hook attempted to set bypassPermissions mode, denied")
				case "acceptEdits":
					deps.SessionPermissions.Mode = setting.ModeAutoAccept
					deps.SetOperationMode(setting.ModeAutoAccept)
				case "dontAsk":
					deps.SessionPermissions.Mode = setting.ModeDontAsk
				case "normal":
					deps.SessionPermissions.Mode = setting.ModeNormal
					deps.SetOperationMode(setting.ModeNormal)
				}
			}
		case "addRules":
			for _, rule := range pu.Rules {
				ruleStr := buildRuleString(rule)
				if ruleStr == "" {
					continue
				}
				if strings.Contains(ruleStr, "(**)") {
					if pu.Destination == "persistent" {
						log.Logger().Warn("hook attempted to persist catch-all rule, denied", gozap.String("rule", ruleStr))
					} else {
						log.Logger().Warn("hook attempted session-scoped catch-all rule, denied", gozap.String("rule", ruleStr))
					}
					continue
				}
				if pu.Destination == "persistent" {
					if err := setting.AddAllowRuleDirectlyAt(ruleStr, deps.Cwd); err != nil {
						log.Logger().Warn("failed to persist hook rule", gozap.Error(err))
					}
					needReload = true
				} else if deps.SessionPermissions != nil {
					deps.SessionPermissions.AllowPattern(ruleStr)
				}
			}
		case "addDirectories":
			if deps.SessionPermissions != nil {
				for _, dir := range pu.Directories {
					deps.SessionPermissions.AddWorkingDirectory(dir)
				}
			}
		}
	}
	if needReload {
		deps.Actions.ReloadProjectContext(deps.Cwd)
	}
}

func buildRuleString(rule hook.PermissionRule) string {
	if rule.RuleContent != "" && rule.ToolName != "" {
		return rule.ToolName + "(" + rule.RuleContent + ":*)"
	}
	if rule.ToolName != "" {
		return rule.ToolName + "(**)"
	}
	if rule.RuleContent != "" {
		return rule.RuleContent
	}
	return ""
}

func ApplyUpdatedToolInput(state *conv.ToolExecState, updated map[string]any) {
	if state.PendingCalls == nil || state.CurrentIdx >= len(state.PendingCalls) {
		return
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return
	}
	state.PendingCalls[state.CurrentIdx].Input = string(data)
}
