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

// MainSession supplies the opened, immutable owner of a main-agent runtime.
// The caller owns opening/closing the session and stopping the runtime before
// replacing it. Review and Recall read live snapshots, not UI state.
type MainSession struct {
	Recorder    *session.Session
	Store       *session.Store
	Settings    PermissionSettings
	Permissions *setting.SessionPermissions
	Hooks       *hook.Engine
	Skills      *skill.Registry
	Review      func() PermissionReview
	Recall      func() *Recall
}

// BuildMainParams binds prompt/provider/workspace inputs to one opened session.
// It owns recording, permission policy, hook authorization and result paging;
// BuildParams retains transport adapters and model/prompt configuration. It does
// not start a runtime, consume its events, or claim persistence succeeded.
func BuildMainParams(p BuildParams, mainSession MainSession) (BuildParams, error) {
	if mainSession.Recorder == nil || mainSession.Store == nil {
		return p, fmt.Errorf("main-session runtime requires an opened session and store")
	}
	if err := mainSession.Recorder.Err(); err != nil {
		return p, err
	}
	if mainSession.Store.TranscriptPath(mainSession.Recorder.ID()) != mainSession.Recorder.TranscriptPath() {
		return p, fmt.Errorf("main-session recorder and result store belong to different projects")
	}
	if mainSession.Settings == nil || mainSession.Permissions == nil {
		return p, fmt.Errorf("main-session runtime requires a permission policy")
	}
	rec := mainSession.Recorder
	p.SessionID = rec.ID()
	p.OnEvent = rec.RecordEvent
	p.HookEngine = nil
	if mainSession.Hooks != nil {
		mainSession.Hooks.SetSession(rec.ID(), rec.TranscriptPath())
		mainSession.Hooks.SetAuditCallback(rec.RecordHookAudit)
		p.HookEngine = mainSession.Hooks
	}
	if mainSession.Skills != nil {
		mainSession.Skills.SetStateChangeObserver(rec.RecordSkillStateChange)
	}
	policy := PermissionPolicy{Settings: mainSession.Settings, Session: mainSession.Permissions, AutoMemory: p.AutoMemory,
		Audit: func(record transcript.Record) { _ = rec.Append(context.Background(), record) },
	}
	p.PermissionRules = policy.Decide
	p.CanApproveByHook = policy.CanApproveByHook
	p.PermissionReview = nil
	if mainSession.Review != nil {
		p.PermissionReview = func(ctx context.Context, name string, args map[string]any, reason string) PermReviewResult {
			return policy.Review(ctx, name, args, reason, mainSession.Review())
		}
	}
	p.ResultFilter = ResultPager{Store: mainSession.Store, SessionID: rec.ID()}.Filter
	if mainSession.Recall != nil {
		p.ResultFilter = WithRecall(p.ResultFilter, mainSession.Recall)
	}
	return p, nil
}
