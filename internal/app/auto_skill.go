// Wires Auto Skill to the current conversation session.
package app

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/autoskill"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/reviewskill"
)

// learnedStoreContext supplies the live workspace to Reflection. It is
// accessed only on the Update loop, including on workspace reload.
type learnedStoreContext struct {
	cwd      string
	settings *setting.Settings
}

func newLearnedStoreContext(cwd string, settings *setting.Settings) *learnedStoreContext {
	return &learnedStoreContext{cwd: cwd, settings: settings}
}

func (c *learnedStoreContext) Snapshot() (string, *setting.Settings) {
	return c.cwd, c.settings
}

func (c *learnedStoreContext) Update(cwd string, settings *setting.Settings) {
	c.cwd = cwd
	c.settings = settings
}

// resolveAutoSkillConfig gates both the reviewer and the ReviewSkill tool.
func (m *model) resolveAutoSkillConfig() (autoskill.Config, error) {
	if m.services.Setting == nil || setting.AutoSkillDisabledByEnv() || !m.services.Setting.ReviewSkillEnabled() {
		return autoskill.Config{}, nil
	}
	return autoskill.ResolveSettings(m.services.Setting.AutoSkill())
}

// wireAutoSkill binds the reviewer to the current session, model and strategy.
func (m *model) wireAutoSkill(params agent.BuildParams) {
	m.teardownAutoSkill()
	cfg, err := m.resolveAutoSkillConfig()
	if err != nil {
		log.Logger().Warn("Auto Skill config rejected", zap.Error(err))
		return
	}
	if !cfg.Enabled() {
		return
	}

	reviewCtx, cancelReview := context.WithCancel(context.Background())
	owner, notices := m.services.Session, m.mainNotices
	runtime, cwd := m.services.Agent, m.env.CWD
	provider, modelID, effort := params.Provider, params.ModelID, params.ThinkingEffort
	settings, config := m.services.Setting, m.services.Setting.AutoSkill()
	authorizeWrite := func() bool {
		return reviewCtx.Err() == nil && runtime.Active() && !setting.AutoSkillDisabledByEnv() &&
			settings.ReviewSkillEnabled() && settings.AutoSkill() == config
	}
	review := func(permissions autoskill.SkillPermissions, snapshot []core.Message) {
		if !authorizeWrite() {
			return
		}
		// Restrict the reviewer to the actions allowed for this turn.
		notice, err := (autoskill.SideReview{
			Provider: provider, Model: modelID, Effort: effort,
			Recording: owner, Skills: autoskill.NewSkillManager(cwd, permissions), Strategy: cfg.Strategy, AuthorizeWrite: authorizeWrite,
		}).Run(reviewCtx, snapshot)
		if reviewCtx.Err() != nil {
			return
		}
		if err != nil {
			log.Logger().Warn("Auto Skill review failed", zap.Error(err))
			return
		}
		queueAutoSkillNotice(reviewCtx, notices, owner, notice)
	}

	m.services.AutoSkill.session = &autoSkillSession{
		reviewer: autoskill.NewSessionReviewer(reviewCtx, cfg, review),
		cancel:   cancelReview,
	}
}

// queueAutoSkillNotice stages a display-only notice without accessing the live
// model. The Update loop rechecks cancellation in case teardown happens after
// enqueue, and normal notice delivery checks the original session owner.
func queueAutoSkillNotice(ctx context.Context, notices chan<- mainNotice, owner *session.Session, notice string) {
	if notice == "" || owner == nil || ctx.Err() != nil {
		return
	}
	select {
	case notices <- mainNotice{Destination: owner, Display: notice, recorded: true, cancelled: ctx.Done()}:
	case <-ctx.Done():
	}
}

// notifyAutoSkillOverride reports actions whose effective permissions differ
// from the settings just saved in the Auto Skill tab.
func (m *model) notifyAutoSkillOverride(msg input.ConfigSavedMsg) {
	snap := m.services.Setting.Snapshot()
	if snap == nil {
		return
	}
	var actions []string
	if msg.SavedAutoSkill.Skills.AllowCreate() != snap.AutoSkill.Skills.AllowCreate() {
		actions = append(actions, "Skill create")
	}
	if msg.SavedAutoSkill.Skills.AllowUpdate() != snap.AutoSkill.Skills.AllowUpdate() {
		actions = append(actions, "Skill update")
	}
	if msg.SavedAutoSkill.Skills.AllowDelete() != snap.AutoSkill.Skills.AllowDelete() {
		actions = append(actions, "Skill delete")
	}
	if len(actions) == 0 {
		return
	}
	other := "project"
	if msg.Scope == setting.ScopeProject {
		other = "user"
	}
	m.conv.AddNotice("Note: " + strings.Join(actions, " and ") +
		" setting is overridden by " + other + "-level settings")
}

// newLearnedSkillStore reads fresh inventory from the live workspace. These
// user-initiated operations aren't subject to the reviewer's permissions, but
// only agent-created skills are shown or editable.
func newLearnedSkillStore(source func() (string, *setting.Settings)) input.LearnedSkillStore {
	mgr := func() *autoskill.SkillManager {
		cwd, _ := source()
		return autoskill.NewSkillManager(cwd, autoskill.AllowAllSkillActions())
	}
	return input.LearnedSkillStore{
		List: func() []autoskill.SkillInfo {
			var out []autoskill.SkillInfo
			for _, s := range mgr().Inventory() {
				if s.Editable() {
					out = append(out, s)
				}
			}
			return out
		},
		Read: func(name string) (string, error) {
			return mgr().Read(name)
		},
		Delete: func(name string) error {
			_, err := mgr().Delete(name)
			return err
		},
	}
}

func (m *model) teardownAutoSkill() {
	if active := m.services.AutoSkill.session; active != nil {
		active.cancel()
		m.services.AutoSkill.session = nil
	}
}

// autoSkillCapabilities determines which actions the ReviewSkill tool exposes.
func (m *model) autoSkillCapabilities() reviewskill.Capabilities {
	cfg, err := m.resolveAutoSkillConfig()
	if err != nil {
		return reviewskill.Capabilities{}
	}
	return reviewskill.Capabilities{
		CreateSkills: cfg.Skills.AllowCreate,
		UpdateSkills: cfg.Skills.AllowUpdate,
		DeleteSkills: cfg.Skills.AllowDelete,
	}
}

func (m *model) autoSkillExtraTools() []core.ToolSchema {
	caps := m.autoSkillCapabilities()
	if !caps.Active() {
		return nil
	}
	return []core.ToolSchema{reviewskill.Schema(caps)}
}
