package agent

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/judge"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
)

// PermissionSettings is the live rule source shared by interactive and print runs.
type PermissionSettings interface {
	DenyRule(string, map[string]any) string
	HasPermissionToUseTool(string, map[string]any, *setting.SessionPermissions) setting.PermissionDecision
	CanApproveByHook(string, map[string]any, *setting.SessionPermissions) bool
}

// PermissionPolicy binds execution authorization to one session. Callers serialize
// decisions with human grant changes and retire the policy with its runtime.
// Audit is best effort; it runs before an approval returns, not as a durability fence.
type PermissionPolicy struct {
	Settings   PermissionSettings
	Session    *setting.SessionPermissions
	AutoMemory system.MemoryStores
	Audit      func(transcript.Record)
}

func (p PermissionPolicy) Decide(name string, args map[string]any) PermDecisionResult {
	if p.Settings == nil || p.Session == nil {
		return PermDecisionResult{Decision: perm.Reject, Reason: "permission policy unavailable"}
	}
	var d setting.PermissionDecision
	if permitAutoMemoryWrite(p.AutoMemory, name, args, p.Settings.DenyRule) {
		d = setting.PermissionDecision{Behavior: perm.Permit, Reason: "auto memory"}
	} else {
		d = p.Settings.HasPermissionToUseTool(name, args, p.Session)
	}
	if d.Behavior == perm.Permit || d.Behavior == perm.Reject {
		p.record(name, args, d.Behavior == perm.Permit, transcript.PermissionSourceConfig, d.Reason)
		return PermDecisionResult{Decision: d.Behavior, Reason: d.Reason}
	}
	return PermDecisionResult{Decision: perm.Prompt, Reason: d.Reason, ToolName: name,
		Description: d.Reason, RequestID: core.NewMessageID(), Reviewable: d.Reviewable}
}

// CanApproveByHook checks hook approval eligibility; it does not invoke a hook or approve.
func (p PermissionPolicy) CanApproveByHook(name string, args map[string]any) bool {
	return p.Settings != nil && p.Session != nil && p.Settings.CanApproveByHook(name, args, p.Session)
}

// PermissionReview is a live snapshot supplied for each gray-zone decision.
// Observe retains tool-call context for UI annotations without owning presentation.
type PermissionReview struct {
	Engaged  bool
	Enabled  bool
	CWD      string
	UnderGit bool
	Mission  string
	Judge    func(context.Context, judge.Request) (judge.Verdict, error)
	Observe  func(context.Context, bool, string)
}

func (p PermissionPolicy) Review(ctx context.Context, name string, args map[string]any, reason string, r PermissionReview) PermReviewResult {
	if !r.Engaged || p.Settings == nil || p.Session == nil || ctx.Err() != nil {
		return PermReviewResult{}
	}
	observe := func(allow bool, reason string) {
		if r.Observe != nil {
			r.Observe(ctx, allow, reason)
		}
	}
	if reason := setting.CircuitBreakerReason(name, args); reason != "" {
		observe(false, reason)
		return PermReviewResult{}
	}
	if reason := setting.UnrecoverableReason(name, args); reason != "" {
		observe(false, reason)
		return PermReviewResult{}
	}
	if !r.Enabled {
		return PermReviewResult{}
	}
	if r.Judge == nil {
		observe(false, "autopilot unavailable — asking you")
		return PermReviewResult{}
	}
	reviewCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	verdict, err := r.Judge(reviewCtx, judge.Request{ToolName: name, Args: args, Reason: reason,
		CWD: r.CWD, Mission: r.Mission, UnderGit: r.UnderGit})
	// A provider may return a verdict after cancellation; it must not create grants.
	if err == nil {
		err = reviewCtx.Err()
	}
	log.Logger().Debug("auto-review verdict", zap.String("tool", name),
		zap.Bool("allow", err == nil && verdict.Allow), zap.String("reason", verdict.Reason), zap.Error(err))
	if err != nil || !verdict.Allow {
		reason := verdict.Reason
		if err != nil || reason == "" {
			reason = "autopilot unavailable — asking you"
		}
		observe(false, reason)
		return PermReviewResult{}
	}
	p.record(name, args, true, transcript.PermissionSourceJudge, verdict.Reason)
	for _, rule := range setting.ExactAllowRules(name, args) {
		p.Session.AllowPattern(rule)
	}
	observe(true, verdict.Reason)
	return PermReviewResult{Allow: true, Reason: "autopilot: " + verdict.Reason}
}

func (p PermissionPolicy) record(name string, args map[string]any, allow bool, source, reason string) {
	if p.Audit == nil {
		return
	}
	input, err := json.Marshal(args)
	if err != nil {
		log.Logger().Warn("marshal permission input", zap.Error(err))
	}
	decision := "reject"
	if allow {
		decision = "permit"
	}
	p.Audit(transcript.Record{Type: transcript.PermissionDecided, Permission: &transcript.PermissionRecord{
		Tool: name, Input: input, Decision: decision, Source: source, Reason: reason,
		Mode: p.Session.CurrentMode().PersistenceName(),
	}})
}
