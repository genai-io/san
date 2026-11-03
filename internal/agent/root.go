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

// RootSession supplies the opened, immutable owner of a main-agent runtime.
// The caller owns opening/closing the session and stopping the runtime before
// replacing it. Review and Recall read live snapshots, not UI state.
type RootSession struct {
	Recorder    *session.Session
	Store       *session.Store
	Settings    PermissionSettings
	Permissions *setting.SessionPermissions
	Hooks       *hook.Engine
	Skills      *skill.Registry
	Review      func() PermissionReview
	Recall      func() *Recall
}

// BuildRootParams binds prompt/provider/workspace inputs to one opened session.
// It owns recording, permission policy, hook authorization and result paging;
// BuildParams retains transport adapters and model/prompt configuration. It does
// not start a runtime, consume its events, or claim persistence succeeded.
func BuildRootParams(p BuildParams, root RootSession) (BuildParams, error) {
	if root.Recorder == nil || root.Store == nil {
		return p, fmt.Errorf("root runtime requires an opened session and store")
	}
	if err := root.Recorder.Err(); err != nil {
		return p, err
	}
	if root.Store.TranscriptPath(root.Recorder.ID()) != root.Recorder.TranscriptPath() {
		return p, fmt.Errorf("root recorder and result store belong to different projects")
	}
	if root.Settings == nil || root.Permissions == nil {
		return p, fmt.Errorf("root runtime requires a permission policy")
	}
	rec := root.Recorder
	p.SessionID = rec.ID()
	p.OnEvent = rec.RecordEvent
	p.HookEngine = nil
	if root.Hooks != nil {
		root.Hooks.SetSession(rec.ID(), rec.TranscriptPath())
		root.Hooks.SetAuditCallback(rec.RecordHookAudit)
		p.HookEngine = root.Hooks
	}
	if root.Skills != nil {
		root.Skills.SetStateChangeObserver(rec.RecordSkillStateChange)
	}
	policy := PermissionPolicy{Settings: root.Settings, Session: root.Permissions, AutoMemory: p.AutoMemory,
		Audit: func(record transcript.Record) { _ = rec.Append(context.Background(), record) },
	}
	p.PermissionRules = policy.Decide
	p.CanApproveByHook = policy.CanApproveByHook
	p.PermissionReview = nil
	if root.Review != nil {
		p.PermissionReview = func(ctx context.Context, name string, args map[string]any, reason string) PermReviewResult {
			return policy.Review(ctx, name, args, reason, root.Review())
		}
	}
	p.ResultFilter = ResultPager{Store: root.Store, SessionID: rec.ID()}.Filter
	if root.Recall != nil {
		p.ResultFilter = WithRecall(p.ResultFilter, root.Recall)
	}
	return p, nil
}
