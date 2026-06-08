// Shared mutable app state: provider, permissions, and cache.
// Pure state holder — no singleton service dependencies.
package app

import (
	"strings"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/filecache"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/setting"
)

type env struct {
	// ── App-level state ─────────────────────────────────────────
	CWD           string
	IsGit         bool
	Width         int
	Height        int
	Ready         bool
	InitialPrompt string

	// ── Provider (mutable — changes via SwitchProvider) ─────────
	LLMProvider  llm.Provider
	CurrentModel *llm.CurrentModelInfo
	// InputTokens / OutputTokens track the latest infer call only.
	// They back the bottom-right context display, so they reflect the most
	// recent prompt/output size rather than a turn or session aggregate.
	InputTokens  int
	OutputTokens int
	// TurnInputTokens / TurnOutputTokens track the current agent turn.
	// A "turn" here means the whole think-act cycle, which may include multiple
	// LLM calls around tool use. These totals are reset at the first infer of a
	// new turn and then accumulated after each infer in that turn.
	TurnInputTokens  int
	TurnOutputTokens int
	turnUsageActive  bool
	ConversationCost llm.Money
	ThinkingEffort   string

	// ── Permission (mutable — changes per mode cycle) ───────────
	OperationMode      setting.OperationMode
	SessionPermissions *setting.SessionPermissions

	// ── Cache (session-scoped) ──────────────────────────────────
	FileCache                 *filecache.Cache
	CachedUserInstructions    string
	CachedProjectInstructions string

	// ── Persistence handle (per-model thinking effort, etc.) ────
	// Held as a field so env-level setters can write through without
	// reaching for package globals; nil-safe in tests that bypass newEnv.
	store *llm.Store
}

func newEnv(llmConn *llm.Conn, cwd string, isGit bool) env {
	e := env{
		CWD:   cwd,
		IsGit: isGit,

		OperationMode:      setting.ModeNormal,
		SessionPermissions: setting.NewSessionPermissions(),

		LLMProvider:  llmConn.Provider(),
		CurrentModel: llmConn.CurrentModel(),

		FileCache: filecache.New(),
		store:     llmConn.Store(),
	}
	// Restore the user's prior per-model thinking-effort choice. Empty
	// means "use provider default" — EffectiveThinkingEffort handles that.
	if e.store != nil && e.CurrentModel != nil {
		e.ThinkingEffort = e.store.GetThinkingEffort(e.CurrentModel.ModelID)
	}
	return e
}

// SetThinkingEffort updates the in-memory thinking-effort selection and
// persists it for the current model. Call this for explicit user choices
// (Ctrl+T, /think); keyword-driven auto-bumps stay in-memory only, so a
// stray "ultrathink" in a prompt doesn't lock the model into the top tier.
func (m *env) SetThinkingEffort(effort string) {
	m.ThinkingEffort = effort
	if m.store == nil || m.CurrentModel == nil {
		return
	}
	if err := m.store.SetThinkingEffort(m.CurrentModel.ModelID, effort); err != nil {
		log.Logger().Warn("persist thinking effort",
			zap.String("model", m.CurrentModel.ModelID),
			zap.String("effort", effort),
			zap.Error(err))
	}
}

// LoadThinkingEffortFromStore refreshes ThinkingEffort from the persisted
// per-model preference. Called after switching models so each model recalls
// its own last-chosen effort.
func (m *env) LoadThinkingEffortFromStore() {
	if m.store == nil || m.CurrentModel == nil {
		m.ThinkingEffort = ""
		return
	}
	m.ThinkingEffort = m.store.GetThinkingEffort(m.CurrentModel.ModelID)
}

func (m *env) GetModelID() string {
	if m.CurrentModel != nil {
		return m.CurrentModel.ModelID
	}
	return ""
}

// GetModelDisplayName returns a human-readable display name for the current
// model by looking it up in the store's cached model list. Falls back to the
// raw model ID if no display name is found.
func (m *env) GetModelDisplayName() string {
	id := m.GetModelID()
	if id == "" {
		return "no model selected"
	}
	if m.store == nil {
		return id
	}
	if name := m.store.CachedModelDisplayName(id); name != "" {
		return name
	}
	return id
}

func (m *env) EffectiveThinkingEffort() string {
	return llm.ResolveThinkingEffort(m.LLMProvider, m.GetModelID(), m.ThinkingEffort)
}

func (m *env) OperationModeName() string {
	switch m.OperationMode {
	case setting.ModeAutoAccept:
		return "auto"
	case setting.ModeBypassPermissions:
		return "bypassPermissions"
	default:
		return "default"
	}
}

func (m *env) ResetSessionPermissions() {
	m.SessionPermissions.AllowAllEdits = false
	m.SessionPermissions.AllowAllWrites = false
	m.SessionPermissions.AllowAllBash = false
	m.SessionPermissions.AllowAllSkills = false
	m.SessionPermissions.Mode = setting.ModeNormal
}

func (m *env) ApplyAutoAcceptPermissions(cwd string) {
	m.SessionPermissions.Mode = setting.ModeAutoAccept
	m.SessionPermissions.AllowAllEdits = true
	m.SessionPermissions.AllowAllWrites = true
	m.SessionPermissions.AddWorkingDirectory(cwd)
}

func (m *env) ApplyBypassPermissions() {
	m.SessionPermissions.Mode = setting.ModeBypassPermissions
}

func (m *env) EnableAutoAcceptMode(cwd string) {
	m.ApplyAutoAcceptPermissions(cwd)
	m.OperationMode = setting.ModeAutoAccept
}

func (m *env) DetectThinkingKeywords(input string) {
	lower := strings.ToLower(input)
	efforts := llm.ThinkingEfforts(m.LLMProvider, m.GetModelID())
	if len(efforts) == 0 {
		return
	}

	if strings.Contains(lower, "ultrathink") ||
		strings.Contains(lower, "think really hard") ||
		strings.Contains(lower, "think super hard") ||
		strings.Contains(lower, "maximum thinking") {
		m.ThinkingEffort = efforts[len(efforts)-1]
		return
	}

	if strings.Contains(lower, "think harder") ||
		strings.Contains(lower, "think hard") ||
		strings.Contains(lower, "think deeply") ||
		strings.Contains(lower, "think carefully") {
		if len(efforts) >= 2 {
			m.ThinkingEffort = efforts[len(efforts)-2]
		}
		return
	}
}

func (m *env) ApplyModePermissions(cwd string) {
	m.ResetSessionPermissions()

	if m.OperationMode == setting.ModeAutoAccept {
		m.ApplyAutoAcceptPermissions(cwd)
	}

	if m.OperationMode == setting.ModeBypassPermissions {
		m.ApplyBypassPermissions()
	}
}

func (m *env) ApplyDefaultPermissionMode(mode string, cwd string, allowBypass bool) {
	opMode := setting.OperationModeFromString(mode)
	if opMode == setting.ModeBypassPermissions && !allowBypass {
		opMode = setting.ModeNormal
	}
	m.OperationMode = opMode
	m.ApplyModePermissions(cwd)
}

func (m *env) ClearCachedInstructions() {
	m.CachedUserInstructions = ""
	m.CachedProjectInstructions = ""
}

func (m *env) SessionMode() string {
	switch m.OperationMode {
	case setting.ModeAutoAccept:
		return "auto-accept"
	default:
		return "normal"
	}
}

func (m *env) ResetContextDisplay() {
	m.InputTokens = 0
	m.OutputTokens = 0
	m.ConversationCost = llm.Money{}
}

func (m *env) ResetTokens() {
	m.ResetContextDisplay()
	m.TurnInputTokens = 0
	m.TurnOutputTokens = 0
	m.turnUsageActive = false
}
