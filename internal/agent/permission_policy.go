package agent

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/judge"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// PermissionSettings is the live rule source shared by interactive and print runs.
type PermissionSettings interface {
	DenyRule(string, map[string]any) string
	HasPermissionToUseTool(string, map[string]any, *setting.SessionPermissions) setting.PermissionDecision
	CanApproveByHook(string, map[string]any, *setting.SessionPermissions) bool
}

// PermissionPolicy binds execution authorization to one session. Callers serialize
// decisions with human grant changes and retire the policy with its runtime.
type PermissionPolicy struct {
	Settings   PermissionSettings
	Session    *setting.SessionPermissions
	AutoMemory system.MemoryStores
}

func (p PermissionPolicy) Decide(name string, args map[string]any) PermDecisionResult {
	if p.Settings == nil || p.Session == nil {
		return PermDecisionResult{Decision: perm.Reject, Reason: "permission policy unavailable"}
	}
	var d setting.PermissionDecision
	if permitAutoMemoryWrite(p.AutoMemory, name, args, p.Settings.DenyRule) {
		d = setting.PermissionDecision{Behavior: perm.Permit, By: "config", Reason: "auto memory"}
	} else {
		d = p.Settings.HasPermissionToUseTool(name, args, p.Session)
	}
	if d.Behavior == perm.Permit || d.Behavior == perm.Reject {
		return PermDecisionResult{Decision: d.Behavior, By: d.By, Scope: d.Scope, Reason: d.Reason}
	}
	return PermDecisionResult{Decision: perm.Prompt, Reason: d.Reason, ToolName: name,
		Description: d.Reason, RequestID: core.NewMessageID(), Judgeable: d.Judgeable}
}

// CanApproveByHook checks hook approval eligibility; it does not invoke a hook or approve.
func (p PermissionPolicy) CanApproveByHook(name string, args map[string]any) bool {
	return p.Settings != nil && p.Session != nil && p.Settings.CanApproveByHook(name, args, p.Session)
}

// JudgeSnapshot is a live snapshot supplied for each gray-zone decision.
// Observe retains tool-call context for UI annotations without owning presentation.
type JudgeSnapshot struct {
	Engaged   bool
	Enabled   bool
	CWD       string
	UnderGit  bool
	Mission   string
	Judge     func(context.Context, judge.Request) (judge.Verdict, error)
	Observe   func(context.Context, bool, string)
	Recording *session.Session
}

func (p PermissionPolicy) Judge(ctx context.Context, name string, args map[string]any, reason string, r JudgeSnapshot) PermJudgeResult {
	if !r.Engaged || p.Settings == nil || p.Session == nil || ctx.Err() != nil {
		return PermJudgeResult{}
	}
	observe := func(allow bool, reason string) {
		if r.Observe != nil {
			r.Observe(ctx, allow, reason)
		}
	}
	if reason := setting.CircuitBreakerReason(name, args); reason != "" {
		observe(false, reason)
		return PermJudgeResult{}
	}
	if reason := setting.UnrecoverableReason(name, args); reason != "" {
		observe(false, reason)
		return PermJudgeResult{}
	}
	if !r.Enabled {
		return PermJudgeResult{}
	}
	if r.Judge == nil {
		observe(false, "autopilot unavailable — asking you")
		return PermJudgeResult{}
	}
	judgeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	scope := r.Recording.Completion(session.PurposeJudge)
	verdict, err := r.Judge(judgeCtx, judge.Request{ToolName: name, Args: args, Reason: reason,
		CWD: r.CWD, Mission: r.Mission, UnderGit: r.UnderGit, Admission: scope.Admission()})
	// A provider may return a verdict after cancellation; it must not create grants.
	if err == nil {
		err = judgeCtx.Err()
	}
	log.Logger().Debug("judge verdict", zap.String("tool", name),
		zap.Bool("allow", err == nil && verdict.Allow), zap.String("reason", verdict.Reason), zap.Error(err))
	if err != nil || !verdict.Allow {
		closeErr := scope.Close()
		var admission *ai.AdmissionError
		var recording *llm.RecordingError
		if closeErr != nil || errors.As(err, &admission) || errors.As(err, &recording) {
			return PermJudgeResult{Err: errors.Join(err, closeErr)}
		}
		reason := verdict.Reason
		if err != nil || reason == "" {
			reason = "autopilot unavailable — asking you"
		}
		observe(false, reason)
		return PermJudgeResult{}
	}
	approve := func() {
		for _, rule := range setting.ExactAllowRules(name, args) {
			p.Session.AllowPattern(rule)
		}
		observe(true, verdict.Reason)
	}
	result := PermJudgeResult{Allow: true, Reason: "autopilot: " + verdict.Reason}
	if scope == nil {
		approve()
	} else {
		result.Finish = func(ctx context.Context, resolved perm.Resolution) error {
			if err := scope.FinishPermission(ctx, resolved); err != nil {
				return err
			}
			if resolved.Decision == "allow" && resolved.By == "judge" {
				approve()
			} else {
				observe(false, resolved.Reason)
			}
			return nil
		}
	}
	return result
}
