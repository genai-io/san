// Opened-session selection and application state records.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	apphook "github.com/genai-io/san/internal/app/hook"
	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/infra/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
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
	opened, err := m.services.SessionStore.NewDraft(context.Background(), session.CreateOptions{ID: m.services.sessionID, AgentID: "main", InitialCWD: m.env.CWD, Config: &config})
	if err != nil {
		return nil, err
	}
	if err := m.replaceSession(opened); err != nil {
		return opened, err
	}
	notices := m.earlyNotices
	m.earlyNotices = nil
	for i, n := range notices {
		if err := opened.Append(context.Background(), session.Event{Payload: noticeAnnotation(n)}); err != nil {
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

func noticeAnnotation(n core.ChatMessage) session.AnnotationAddPayload {
	p := session.AnnotationAddPayload{Text: n.Content, ByAgent: n.AgentNotice}
	if n.AutoMemoryRecallNotice != nil {
		p.Text = chat.EncodeRecallNotice(n.AutoMemoryRecallNotice)
		p.AfterMessage = n.AutoMemoryRecallNotice.AfterMessageID
	}
	return p
}

func (m *model) recordNotice(n core.ChatMessage) {
	if opened := m.services.Session; opened != nil {
		if err := opened.Append(context.Background(), session.Event{Payload: noticeAnnotation(n)}); err != nil {
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
		m.retireFlowMain()
		m.resetRecap()
		m.cancelMemoryUpdate()
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
		m.resetUserPrompts()
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
	apphook.Bind(m.services.Hook, m.env.LLMProvider, opened, m.env.GetModelID())
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
		maxTokens = widgets.GetMaxOutput(m.services.LLM.Store(), m.env.CurrentModel, setting.DefaultMaxTokens)
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
		dir := task.ListDir(homeDir, taskListID)
		m.setTrackerStorageDir(dir)
		m.setTaskOutputDir(dir)
		return
	}
	if sessionID == "" {
		return
	}
	dir := task.ListDir(homeDir, sessionID)
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
	opened, err := m.services.Session.Fork(context.Background(), session.ForkOptions{})
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
		if err := opened.SetAttrs(context.Background(), session.AttrsPatch{Title: session.SetField(name)}); err != nil {
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
	opened, err := m.services.SessionStore.NewDraft(context.Background(), session.CreateOptions{ID: session.NewSessionID(), AgentID: "main", InitialCWD: m.env.CWD})
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
