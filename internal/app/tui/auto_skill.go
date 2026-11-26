// Wires Auto Skill to the current conversation session.
package tui

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/infra/log"
	reflectskill "github.com/genai-io/san/internal/reflect/skill"
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
func (m *model) resolveAutoSkillConfig() (reflectskill.Config, error) {
	if m.services.Setting == nil || setting.AutoSkillDisabledByEnv() || !m.services.Setting.ReviewSkillEnabled() {
		return reflectskill.Config{}, nil
	}
	return reflectskill.ResolveSettings(m.services.Setting.AutoSkill())
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
	review := func(permissions reflectskill.SkillPermissions, snapshot []core.Message) {
		if !authorizeWrite() {
			return
		}
		// Restrict the reviewer to the actions allowed for this turn.
		notice, err := (reflectskill.SideReview{
			Provider: provider, Model: modelID, Effort: effort,
			Recording: owner, Skills: reflectskill.NewSkillManager(cwd, permissions), Strategy: cfg.Strategy, AuthorizeWrite: authorizeWrite,
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
		reviewer: reflectskill.NewSessionReviewer(reviewCtx, cfg, review),
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
	live := m.services.Setting.AutoSkill().Skills
	var actions []string
	if msg.SavedAutoSkill.Skills.AllowCreate() != live.AllowCreate() {
		actions = append(actions, "Skill create")
	}
	if msg.SavedAutoSkill.Skills.AllowUpdate() != live.AllowUpdate() {
		actions = append(actions, "Skill update")
	}
	if msg.SavedAutoSkill.Skills.AllowDelete() != live.AllowDelete() {
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
	mgr := func() *reflectskill.SkillManager {
		cwd, _ := source()
		return reflectskill.NewSkillManager(cwd, reflectskill.AllowAllSkillActions())
	}
	return input.LearnedSkillStore{
		List: func() []reflectskill.SkillInfo {
			var out []reflectskill.SkillInfo
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
