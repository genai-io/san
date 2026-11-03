package agent

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/transcript"
)

// PrimarySession supplies the opened, immutable owner of a main-agent runtime.
// The caller owns opening/closing the session and stopping the runtime before
// replacing it. Review and Recall read live snapshots, not UI state.
type PrimarySession struct {
	Recorder    *session.Session
	Store       *session.Store
	Settings    PermissionSettings
	Permissions *setting.SessionPermissions
	Hooks       *hook.Engine
	Skills      *skill.Registry
	Review      func() PermissionReview
	Recall      func() *Recall
}

// BuildPrimaryParams binds prompt/provider/workspace inputs to one opened session.
// It owns recording, permission policy, hook authorization and result paging;
// BuildParams retains transport adapters and model/prompt configuration. It does
// not start a runtime, consume its events, or claim persistence succeeded.
func BuildPrimaryParams(p BuildParams, primary PrimarySession) (BuildParams, error) {
	if primary.Recorder == nil || primary.Store == nil {
		return p, fmt.Errorf("primary runtime requires an opened session and store")
	}
	if err := primary.Recorder.Err(); err != nil {
		return p, err
	}
	if primary.Store.TranscriptPath(primary.Recorder.ID()) != primary.Recorder.TranscriptPath() {
		return p, fmt.Errorf("primary recorder and result store belong to different projects")
	}
	if primary.Settings == nil || primary.Permissions == nil {
		return p, fmt.Errorf("primary runtime requires a permission policy")
	}
	rec := primary.Recorder
	p.SessionID = rec.ID()
	p.OnEvent = rec.RecordEvent
	p.HookEngine = nil
	if primary.Hooks != nil {
		primary.Hooks.SetSession(rec.ID(), rec.TranscriptPath())
		primary.Hooks.SetAuditCallback(rec.RecordHookAudit)
		p.HookEngine = primary.Hooks
	}
	if primary.Skills != nil {
		primary.Skills.SetStateChangeObserver(rec.RecordSkillStateChange)
	}
	policy := PermissionPolicy{Settings: primary.Settings, Session: primary.Permissions, AutoMemory: p.AutoMemory,
		Audit: func(record transcript.Record) { _ = rec.Append(context.Background(), record) },
	}
	p.PermissionRules = policy.Decide
	p.CanApproveByHook = policy.CanApproveByHook
	p.PermissionReview = nil
	if primary.Review != nil {
		p.PermissionReview = func(ctx context.Context, name string, args map[string]any, reason string) PermReviewResult {
			return policy.Review(ctx, name, args, reason, primary.Review())
		}
	}
	p.ResultFilter = ResultPager{Store: primary.Store, SessionID: rec.ID()}.Filter
	if primary.Recall != nil {
		p.ResultFilter = WithRecall(p.ResultFilter, primary.Recall)
	}
	return p, nil
}
