// Opened-session selection, application state records, and native history restoration.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func (m *model) sessionID() string {
	if m.services.Session != nil {
		return m.services.Session.ID()
	}
	return m.services.sessionID
}

func (m *model) sessionTranscriptPath() string {
	if m.services.Session != nil {
		return m.services.Session.Path()
	}
	if m.services.SessionStore == nil || m.sessionID() == "" {
		return ""
	}
	return filepath.Join(m.services.SessionStore.ProjectDir(), "sessions", m.sessionID(), "events.jsonl")
}

func (m *model) ensureSessionStore() error {
	if m.services.SessionStore != nil {
		return nil
	}
	store, err := session.OpenStore(context.Background(), m.env.CWD)
	if err != nil {
		return err
	}
	m.services.SessionStore = store
	return nil
}

func (m *model) openCurrentSession() (*session.Session, error) {
	if m.services.Session != nil {
		return m.services.Session, m.services.Session.Err()
	}
	if err := m.ensureSessionStore(); err != nil {
		return nil, err
	}
	if m.services.sessionID == "" {
		m.services.sessionID = session.NewSessionID()
	}
	update := m.buildSessionRecord()
	config := session.EventConfig{}
	if update.Config != nil {
		patch := update.Config
		config.Provider = patch.Provider.Value
		config.Model = patch.Model.Value
		config.GenerationOptions = patch.GenerationOptions.Value
		config.Mode = patch.Mode.Value
		config.AutopilotPolicy = patch.AutopilotPolicy.Value
	}
	opened, err := m.services.SessionStore.Create(context.Background(), session.CreateOptions{ID: m.services.sessionID, AgentID: "main", InitialCWD: m.env.CWD, Config: &config})
	if err != nil {
		return nil, err
	}
	if err := m.replaceSession(opened); err != nil {
		return opened, err
	}
	notices := m.earlyNotices
	m.earlyNotices = nil
	for i, n := range notices {
		if err := opened.Append(context.Background(), session.Event{Payload: session.AnnotationAddPayload{Text: n.Content, ByAgent: n.AgentNotice, AfterMessage: conv.MemoryRecallNoticeAnchor(n.Content)}}); err != nil {
			m.earlyNotices = append(m.earlyNotices, notices[i:]...)
			return opened, err
		}
	}
	return opened, nil
}

func (m *model) appendSessionState() error {
	opened, err := m.openCurrentSession()
	if err != nil {
		return err
	}
	return session.NewRecorder(opened, "").RecordState(context.Background(), *m.buildSessionRecord())
}

func (m *model) recordNotice(n core.ChatMessage) {
	if opened := m.services.Session; opened != nil {
		if err := opened.Append(context.Background(), session.Event{Payload: session.AnnotationAddPayload{Text: n.Content, ByAgent: n.AgentNotice, AfterMessage: conv.MemoryRecallNoticeAnchor(n.Content)}}); err != nil {
			log.Logger().Warn("session annotation append failed", zap.Error(err))
		}
		return
	}
	m.earlyNotices = append(m.earlyNotices, n)
}

// replaceSession fences owner generations, even when a reopened handle has the
// same ID. Old hook work is joined while its original writer is still available.
func (m *model) replaceSession(opened *session.Session) error {
	if opened == nil {
		return fmt.Errorf("cannot select a nil session")
	}
	previous := m.services.Session
	changed := previous != opened || m.sessionID() != opened.ID()
	retiring := previous != nil || m.sessionID() != opened.ID()
	var closeErr error
	if changed {
		m.resetRecap()
		m.cancelAutopilot()
		m.cancelGroupControl()
		if err := m.cancelManualCompaction(); err != nil {
			return err
		}
		if oldHooks := m.services.Hook; oldHooks != nil {
			next := oldHooks.ForSession(opened.ID(), m.env.CWD, opened.Path())
			oldHooks.Shutdown()
			m.services.Hook = next
		}
		if previous != nil {
			closeErr = previous.Close()
		}
	}
	if changed && retiring {
		m.env.SessionPermissions = setting.NewSessionPermissions()
		m.env.ApplyModePermissions(m.env.CWD)
		m.discardPendingMessages()
		m.queueContinuation = nil
		m.pendingPluginRoot = ""
		m.resetInteractions()
		if m.services.Reminder != nil {
			m.services.Reminder.DiscardPendingNotices()
		}
		m.conv.SetAgentIdentitySession(opened.ID())
	}
	m.services.Session = opened
	m.services.sessionID = opened.ID()
	if changed {
		m.bindMainBroker(opened)
	}
	group.BindSession(opened.ID)
	bindSessionHooks(m.services.Hook, m.env.LLMProvider, opened, m.env.GetModelID())
	if changed && m.services.Hook != nil {
		m.configureAsyncHookCallback()
		m.wireTaskLifecycle(m.services.Hook)
	}
	return closeErr
}

func (m *model) appendTurnState() error {
	err := m.appendSessionState()
	if err != nil {
		log.Logger().Warn("session state append failed", zap.Error(err))
		m.conv.AddNotice("Session was not saved: " + err.Error())
	}
	return err
}

// buildSessionRecord captures product state only. Recorder.RecordState compares
// it with the committed projection; no message snapshot is ever written here.
func (m *model) buildSessionRecord() *session.SessionUpdatePayload {
	provider := ""
	if m.env.LLMProvider != nil {
		provider = m.env.LLMProvider.Name()
	}
	maxTokens := setting.DefaultMaxTokens
	if m.services.LLM != nil {
		maxTokens = kit.GetMaxOutput(m.services.LLM.Store(), m.env.CurrentModel, setting.DefaultMaxTokens)
	}
	generation := session.GenerationOptions{Version: 1}
	if m.services.Session != nil {
		p := m.services.Session.Snapshot()
		if saved := p.Config().GenerationOptions; saved != nil {
			generation = *saved
		}
	}
	if generation.MaxTokens == nil {
		generation.MaxTokens = &maxTokens
	}
	generation.Effort = ai.Effort(m.env.EffectiveThinkingEffort())
	policy := m.env.AutoPilot.Clone()
	policy.MissionState = ""
	update := &session.SessionUpdatePayload{
		Config: &session.ConfigPatch{
			Provider: session.SetField(provider), Model: session.SetField(m.env.GetModelID()),
			GenerationOptions: session.SetField(generation), Mode: session.SetField(m.env.SessionMode()),
			AutopilotPolicy: session.SetField(session.AutopilotPolicy{Version: 1, Settings: policy}),
		},
		State: &session.StatePatch{
			ExecutionCWD: session.SetField(m.env.CWD), GroupHint: session.ClearField[session.GroupHint](),
			AutopilotProgress: session.SetField(session.AutopilotProgress{
				CompletedTurns: m.autopilotContinuations, Status: string(m.env.AutoPilot.MissionState),
			}),
		},
	}
	if m.services.Tracker != nil {
		update.State.Todos = session.SetField(m.services.Tracker.Export())
	}
	if name, self := group.Current(); name != "" {
		update.State.GroupHint = session.SetField(session.GroupHint{GroupID: name, MemberID: self.Name})
	}
	if m.services.Session != nil {
		p := m.services.Session.Snapshot()
		if p.State().Title == nil {
			if title := session.GenerateTitle(p.Messages()); title != "" {
				update.State.Title = session.SetField(title)
			}
		}
	}
	return update
}

// validateResumeDirectory rejects implicit execution-directory changes; it
// neither grants trust nor changes the current process working directory.
func validateResumeDirectory(saved, requested string) error {
	if saved == "" {
		return nil
	}
	canonical := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(absolute)
	}
	persisted, err := canonical(saved)
	if err != nil {
		return fmt.Errorf("cannot validate saved session directory %q: %w; restore that directory before resuming", saved, err)
	}
	current, err := canonical(requested)
	if err != nil {
		return fmt.Errorf("cannot validate requested session directory %q: %w", requested, err)
	}
	if persisted != current {
		return fmt.Errorf("session directory mismatch: saved directory %q differs from requested directory %q; reopen San in the saved directory to resume (implicit directory switching is not supported)", saved, requested)
	}
	return nil
}

func (m *model) loadSessionByID(id string) error {
	if err := m.ensureSessionStore(); err != nil {
		return err
	}
	if err := m.StopAgentRuntime(); err != nil {
		return err
	}
	if m.workerMu != nil {
		m.workerMu.Lock()
		defer m.workerMu.Unlock()
	}
	history, err := m.services.SessionStore.Read(context.Background(), id)
	if err != nil {
		return err
	}
	saved := history.Identity().InitialCWD
	if cwd := history.State().ExecutionCWD; cwd != nil {
		saved = *cwd
	}
	if err := validateResumeDirectory(saved, m.env.CWD); err != nil {
		return err
	}
	// Validate presentation metadata before changing the selected owner too.
	if _, err := m.services.SessionStore.Attrs(context.Background(), id); err != nil {
		return err
	}
	if _, _, err := m.resolveSessionModel(history); err != nil {
		return err
	}
	opened := m.services.Session
	if opened != nil && opened.ID() == id && opened.Err() == nil && m.services.Hook != nil {
		oldHooks := m.services.Hook
		next := oldHooks.ForSession(opened.ID(), m.env.CWD, opened.Path())
		oldHooks.Shutdown()
		m.services.Hook = next
		bindSessionHooks(next, m.env.LLMProvider, opened, m.env.GetModelID())
		m.configureAsyncHookCallback()
		m.wireTaskLifecycle(next)
		history = opened.Snapshot()
	}
	if opened == nil || opened.ID() != id || opened.Err() != nil {
		if opened != nil && opened.ID() == id {
			if m.services.Hook != nil {
				m.services.Hook.Shutdown()
			}
			if err := opened.Close(); err != nil {
				return err
			}
		}
		opened, err = m.services.SessionStore.Open(context.Background(), id)
		if err != nil {
			return err
		}
		history = opened.Snapshot()
		saved = history.Identity().InitialCWD
		if cwd := history.State().ExecutionCWD; cwd != nil {
			saved = *cwd
		}
		if err := validateResumeDirectory(saved, m.env.CWD); err != nil {
			return errors.Join(err, opened.Close())
		}
		if _, _, err := m.resolveSessionModel(history); err != nil {
			return errors.Join(err, opened.Close())
		}
	}
	if err := m.replaceSession(opened); err != nil {
		return err
	}
	fs.ResetFileViews()
	m.env.FileCache.Clear()
	if err := m.restoreHistory(history); err != nil {
		return err
	}
	m.env.ResetContextDisplay()
	m.ReconfigureAgentTool()
	return nil
}

func (m *model) resolveSessionModel(history session.Projection) (llm.Provider, *llm.CurrentModelInfo, error) {
	config := history.Config()
	provider := m.env.LLMProvider
	info := &llm.CurrentModelInfo{}
	if m.env.CurrentModel != nil {
		*info = *m.env.CurrentModel
	}
	if saved := config.Provider; saved != nil && *saved != "" {
		if provider == nil || provider.Name() != *saved {
			if m.services.LLM == nil {
				return nil, nil, fmt.Errorf("cannot resume provider %q without connection settings", *saved)
			}
			resolved, err := llm.NewProviderPool(m.services.LLM.Store()).Resolve(context.Background(), llm.ProviderID(*saved))
			if err != nil {
				return nil, nil, fmt.Errorf("cannot resume provider %q: %w", *saved, err)
			}
			provider = resolved
			info.Provider = llm.ProviderID(*saved)
			if connection, ok := m.services.LLM.Store().Connection(info.Provider); ok {
				info.AuthMethod = connection.AuthMethod
			}
		}
	}
	if provider != nil {
		info.Provider = llm.ProviderID(provider.Name())
	}
	if saved := config.Model; saved != nil {
		info.ModelID = *saved
	}
	return provider, info, nil
}

func (m *model) restoreHistory(history session.Projection) error {
	provider, info, err := m.resolveSessionModel(history)
	if err != nil {
		return err
	}
	attrs, err := m.services.SessionStore.Attrs(context.Background(), history.SessionID())
	if err != nil {
		return err
	}
	m.env.LLMProvider, m.env.CurrentModel = provider, info
	if m.services.LLM != nil {
		m.services.LLM.SetProvider(provider)
		m.services.LLM.SetCurrentModel(info)
	}
	bindSessionHooks(m.services.Hook, provider, m.services.Session, info.ModelID)
	m.resetRecap()
	m.conv.Messages = m.services.SessionStore.ChatRows(history)
	m.earlyNotices = nil
	m.services.Agent.SetMessages(history.ActiveMessages())
	m.services.Reminder.RestoreHistory(m.conv.Messages)
	m.conv.CommittedCount = 0
	m.env.SessionName = attrs.Title
	state, config := history.State(), history.Config()
	if m.env.SessionName == "" && state.Title != nil {
		m.env.SessionName = *state.Title
	}
	m.setTrackerStorageDir("")
	m.initTaskStorage(history.SessionID())
	m.services.Tracker.Reset()
	if state.Todos != nil {
		m.services.Tracker.Import(*state.Todos)
	}
	m.beforeGoal = nil
	m.env.AutoPilot = autopilotWithoutSessionFields(m.services.Setting.Snapshot().AutoPilot)
	if config.AutopilotPolicy != nil {
		m.env.AutoPilot = config.AutopilotPolicy.Settings.Clone()
	}
	m.autopilotContinuations = 0
	if state.AutopilotProgress != nil {
		m.autopilotContinuations = state.AutopilotProgress.CompletedTurns
		m.env.AutoPilot.MissionState = setting.MissionState(state.AutopilotProgress.Status)
	}
	if m.env.AutoPilot.MissionState == setting.MissionRunning {
		m.env.AutoPilot.MissionState = setting.MissionPaused
	}
	m.rebuildAutopilotJudge()
	if generation := config.GenerationOptions; generation != nil {
		m.env.ThinkingEffort = string(generation.Effort)
	}
	membershipJSON := ""
	if hint := state.GroupHint; hint != nil {
		data, _ := json.Marshal(membership{Group: hint.GroupID, Name: hint.MemberID})
		membershipJSON = string(data)
	}
	savedMode := ""
	if config.Mode != nil {
		savedMode = *config.Mode
	}
	if mode := resumeOperationMode(savedMode, m.env.OperationMode); mode != m.env.OperationMode {
		m.env.OperationMode = mode
		m.applyOperationMode()
	}
	m.rejoinGroup(membershipJSON)
	unfinished := history.Unfinished()
	// Interrupted-operation status is reconstructed presentation, not a new
	// annotation fact on every resume. Never send it into the native context.
	statusNotice := func(text string) {
		m.conv.Messages = append(m.conv.Messages, core.ChatMessage{Role: core.ChatNotice, Content: text})
	}
	if len(unfinished.Inferences) > 0 {
		statusNotice(fmt.Sprintf("Resumed with %d unfinished model call(s); no response was committed and no call was retried.", len(unfinished.Inferences)))
	}
	for _, call := range unfinished.NeverRan {
		statusNotice("Tool call " + call + " did not pass its permission gate; it was not run.")
	}
	for _, call := range unfinished.Unknown {
		statusNotice("Tool call " + call + " was allowed but has no committed completion; its outcome is unknown and it was not retried.")
	}
	return nil
}

func (m *model) initTaskStorage(sessionID string) {
	m.services.Task.SetSessionID(sessionID)
	if m.services.Tracker.StorageDir() != "" {
		return
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Logger().Warn("failed to get home directory for task storage", zap.Error(err))
		return
	}
	taskListID := setting.Getenv("TASK_LIST_ID")
	if taskListID != "" {
		dir := filepath.Join(confdir.Dir(homeDir), "tasks", taskListID)
		m.setTrackerStorageDir(dir)
		m.setTaskOutputDir(dir)
		return
	}
	if sessionID == "" {
		return
	}
	dir := filepath.Join(confdir.Dir(homeDir), "tasks", sessionID)
	m.setTrackerStorageDir(dir)
	m.setTaskOutputDir(dir)
}

func (m *model) setTrackerStorageDir(dir string) {
	if err := m.services.Tracker.SetStorageDir(dir); err != nil {
		log.Logger().Warn("task tracker storage unavailable", zap.String("dir", dir), zap.Error(err))
	}
}

func (m *model) setTaskOutputDir(dir string) {
	outputs := filepath.Join(dir, "outputs")
	if err := m.services.Task.SetOutputDir(outputs); err != nil {
		log.Logger().Warn("task output directory unavailable", zap.String("dir", outputs), zap.Error(err))
	}
}

func (m *model) forkSession() (string, error) {
	if m.sessionID() == "" {
		return "", fmt.Errorf("no active session to fork")
	}
	if err := m.StopAgentRuntime(); err != nil {
		return "", err
	}
	if m.workerMu != nil {
		m.workerMu.Lock()
		defer m.workerMu.Unlock()
	}
	if err := m.appendSessionState(); err != nil {
		return "", err
	}
	originalID := m.sessionID()
	opened, err := m.services.SessionStore.Fork(context.Background(), originalID, session.ForkOptions{})
	if err != nil {
		return "", err
	}
	if err := m.replaceSession(opened); err != nil {
		return originalID, err
	}
	fs.ResetFileViews()
	m.env.FileCache.Clear()
	if err := m.restoreHistory(opened.Snapshot()); err != nil {
		return originalID, err
	}
	m.env.ResetContextDisplay()
	m.ReconfigureAgentTool()
	return originalID, nil
}

func (m *model) renameSession(name, role string) (string, error) {
	opened, err := m.openCurrentSession()
	if err != nil {
		return "", err
	}
	if name != "" {
		if err := m.services.SessionStore.SetAttrs(context.Background(), opened.ID(), session.AttrsPatch{Title: session.SetField(name)}); err != nil {
			return "", err
		}
		m.env.SessionName = name
	}
	note := m.renameMember(name, role)
	return note, m.appendSessionState()
}

func (m *model) clearSession() error {
	if err := m.ResetAgentRuntime(); err != nil {
		return err
	}
	if m.workerMu != nil {
		m.workerMu.Lock()
		defer m.workerMu.Unlock()
	}
	if err := m.ensureSessionStore(); err != nil {
		return err
	}
	opened, err := m.services.SessionStore.Create(context.Background(), session.CreateOptions{ID: session.NewSessionID(), AgentID: "main", InitialCWD: m.env.CWD})
	if err != nil {
		return err
	}
	if err := m.replaceSession(opened); err != nil {
		return err
	}
	group.Release()
	m.grp = groupState{polling: m.grp.polling}
	m.env.SessionName = ""
	m.earlyNotices = nil
	m.beforeGoal = nil
	m.autopilotContinuations = 0
	m.env.AutoPilot = autopilotWithoutSessionFields(m.services.Setting.Snapshot().AutoPilot)
	m.rebuildAutopilotJudge()
	m.setTrackerStorageDir("")
	m.initTaskStorage(m.sessionID())
	m.services.Tracker.Reset()
	fs.ResetFileViews()
	m.env.FileCache.Clear()
	m.ReconfigureAgentTool()
	return m.appendSessionState()
}
