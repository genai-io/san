// Opened-session selection, application state records, and native history restoration.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/transcript"
)

// sessionID also names a new conversation before its transcript is opened.
func (m *model) sessionID() string {
	if m.services.Session != nil {
		return m.services.Session.ID()
	}
	return m.services.sessionID
}

func (m *model) sessionTranscriptPath() string {
	if m.services.SessionStore == nil || m.sessionID() == "" {
		return ""
	}
	return m.services.SessionStore.TranscriptPath(m.sessionID())
}

func (m *model) ensureSessionStore() error {
	if m.services.SessionStore != nil {
		return nil
	}
	store, err := session.NewStore(m.env.CWD)
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
	var provider string
	if m.env.LLMProvider != nil {
		provider = m.env.LLMProvider.Name()
	}
	maxOutputTokens := setting.DefaultMaxTokens
	if m.services.LLM != nil {
		maxOutputTokens = kit.GetMaxOutput(m.services.LLM.Store(), m.env.CurrentModel, setting.DefaultMaxTokens)
	}
	if m.services.sessionID == "" {
		m.services.sessionID = session.NewSessionID()
		id := m.services.sessionID
		group.BindSession(func() string { return id })
	}
	opened, err := m.services.SessionStore.Create(session.CreateOptions{ID: m.services.sessionID,
		AgentID: "main", Provider: provider, Model: m.env.GetModelID(), MaxTokens: maxOutputTokens})
	if err != nil {
		return nil, err
	}
	m.replaceSession(opened)
	return opened, nil
}

func (m *model) appendSessionState() error {
	record := m.buildSessionRecord()
	if record == nil {
		return nil
	}
	opened, err := m.openCurrentSession()
	if err != nil {
		return err
	}
	return opened.Append(context.Background(), *record)
}

func (m *model) recordNotice(n core.ChatMessage) {
	if opened := m.services.Session; opened != nil {
		_ = opened.Append(context.Background(), transcript.Record{Type: transcript.NoticeAdded,
			Notice: &transcript.NoticeRecord{Text: n.Content, Agent: n.AgentNotice,
				AfterMessageID: conv.MemoryRecallNoticeAnchor(n.Content)}})
		return
	}
	m.earlyNotices = append(m.earlyNotices, n)
}

// replaceSession retires the prior session and UI context after opening the replacement.
func (m *model) replaceSession(opened *session.Session) {
	if m.sessionID() != opened.ID() {
		m.conv.SetAgentIdentitySession(opened.ID())
		m.env.SessionPermissions = setting.NewSessionPermissions()
		m.env.ApplyModePermissions(m.env.CWD)
		m.pendingMessages = nil
		m.cancellingTurn = nil
		m.queueContinuation = nil
		m.pendingPluginRoot = ""
		m.resetInteractions()
		m.services.Reminder.DiscardPendingNotices()
	}
	if previous := m.services.Session; previous != opened {
		_ = previous.Close()
	}
	m.services.Session = opened
	m.services.sessionID = opened.ID()
	group.BindSession(opened.ID)
	if m.services.Hook != nil {
		m.services.Hook.SetSession(m.sessionID(), m.sessionTranscriptPath())
	}
}

func (m *model) appendTurnState() error {
	err := m.appendSessionState()
	if err != nil {
		log.Logger().Warn("session state append failed", zap.Error(err))
		m.conv.AddNotice("Session was not saved: " + err.Error())
	}
	return err
}

// buildSessionRecord contains application state only, never the message chain.
func (m *model) buildSessionRecord() *transcript.Record {
	if len(m.conv.Messages) == 0 {
		return nil
	}
	messages := m.services.Agent.Messages()
	title := m.env.SessionName
	if title == "" {
		title = session.GenerateTitle(messages)
	}
	return &transcript.Record{Type: transcript.SessionStatePatched,
		State: &transcript.StatePatch{
			Title: new(title), LastPrompt: new(session.ExtractLastUserText(messages)),
			Mode: new(m.env.SessionMode()), AutoPilot: new(marshalAutoPilot(m.env.AutoPilot)),
			Group: new(currentMembership()), Tasks: new(m.services.Tracker.Export()),
		}}
}

func (m *model) loadSessionByID(id string) error {
	if err := m.ensureSessionStore(); err != nil {
		return err
	}

	// Finish pending writes before reading, including a resume of this same ID.
	m.StopAgentRuntime()
	if m.services.Agent.Active() {
		return fmt.Errorf("previous agent is still stopping")
	}
	if m.workerMu != nil {
		m.workerMu.Lock()
		defer m.workerMu.Unlock()
	}
	var opened *session.Session
	var sess *session.History
	var err error
	if m.services.Session != nil && m.sessionID() == id && m.services.Session.Err() == nil {
		opened = m.services.Session
		sess, err = m.services.SessionStore.Read(id)
	} else {
		if m.services.Session != nil && m.sessionID() == id {
			_ = m.services.Session.Close()
			m.services.Session = nil
		}
		opened, sess, err = m.services.SessionStore.Open(id)
	}
	if err != nil {
		return err
	}

	// This is a different persisted conversation. Do not let a stopped agent's
	// restart snapshot override the selected transcript on the next turn, and
	// don't let the previous conversation's Reads satisfy Edit's
	// read-before-modify gate in this one, nor its files ride on compaction.
	m.replaceSession(opened)
	fs.ResetFileViews()
	m.env.FileCache.Clear()
	m.restoreHistory(sess)

	if len(sess.Tasks) == 0 {
		m.services.Tracker.Reset()
	}

	m.env.ResetContextDisplay()
	m.ReconfigureAgentTool()

	return nil
}

func (m *model) restoreHistory(sess *session.History) {
	m.conv.Messages = m.services.SessionStore.ChatRows(sess)
	m.earlyNotices = nil
	m.services.Agent.SetMessages(sess.Messages)
	m.systemRemindersSent = slices.ContainsFunc(m.conv.Messages, func(msg core.ChatMessage) bool {
		return msg.Role == core.ChatUser && reminder.HasSystemReminder(msg.Content)
	})
	m.conv.CommittedCount = 0

	m.env.SessionName = sess.Metadata.Title

	// Leave any earlier session's tracker directory behind: initTaskStorage
	// keeps a directory that is already set, and this session's items are
	// re-imported below after SetStorageDir's disk load.
	m.setTrackerStorageDir("")
	m.initTaskStorage(m.sessionID())

	if len(sess.Tasks) > 0 {
		m.services.Tracker.Import(sess.Tasks)
	}

	// Session-specific settings never inherit the previous conversation's goal.
	m.beforeGoal = nil
	m.env.AutoPilot = autopilotWithoutSessionFields(m.services.Setting.Snapshot().AutoPilot)
	if ar := parseAutoPilot(sess.Metadata.AutoPilot); !ar.IsZero() {
		m.env.AutoPilot = ar
	}
	if m.env.AutoPilot.MissionState == setting.MissionRunning {
		m.env.AutoPilot.MissionState = setting.MissionPaused
	}
	m.rebuildAutopilotReviewer()

	// Membership follows the session: rejoin the group it was in, and leave the
	// one the previous session held offline.
	m.rejoinGroup(sess.Metadata.Group)

	// Resume into the operation mode the session was saved in, so an autopilot
	// run picks up where it left off without re-cycling shift+tab. Bypass is
	// never granted by a session, only kept — see resumeOperationMode.
	if mode := resumeOperationMode(sess.Metadata.Mode, m.env.OperationMode); mode != m.env.OperationMode {
		m.env.OperationMode = mode
		m.applyOperationMode()
	}
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

// setTrackerStorageDir points the task tracker at dir. A failure leaves the
// tracker working in memory but no longer persisting, which is worth a line in
// the log — there is nothing the user can do about it mid-session, and failing
// the session load over it would be worse than losing the task file.
func (m *model) setTrackerStorageDir(dir string) {
	if err := m.services.Tracker.SetStorageDir(dir); err != nil {
		log.Logger().Warn("task tracker storage unavailable",
			zap.String("dir", dir), zap.Error(err))
	}
}

// setTaskOutputDir points background-task output at dir/outputs, with the same
// degrade-and-log answer as setTrackerStorageDir.
func (m *model) setTaskOutputDir(dir string) {
	outputs := filepath.Join(dir, "outputs")
	if err := m.services.Task.SetOutputDir(outputs); err != nil {
		log.Logger().Warn("task output directory unavailable",
			zap.String("dir", outputs), zap.Error(err))
	}
}

func (m *model) forkSession() (string, error) {
	if m.sessionID() == "" {
		return "", fmt.Errorf("no active session to fork")
	}
	m.StopAgentRuntime()
	if m.services.Agent.Active() {
		return "", fmt.Errorf("agent is still stopping")
	}
	if m.workerMu != nil {
		m.workerMu.Lock()
		defer m.workerMu.Unlock()
	}
	opened, forked, err := m.services.SessionStore.Fork(m.sessionID())
	if err != nil {
		return "", err
	}
	originalID := forked.Metadata.ParentSessionID
	m.replaceSession(opened)
	m.releaseGroupOnSwitch()
	m.services.Agent.SetMessages(forked.Messages)
	// Same storage switch as loadSessionByID: clear the tracker so
	// initTaskStorage adopts the fork's directory, then re-import the tasks
	// that SetStorageDir's disk load replaced.
	m.setTrackerStorageDir("")
	m.initTaskStorage(forked.Metadata.ID)
	m.services.Tracker.Import(forked.Tasks)
	m.ReconfigureAgentTool()
	return originalID, nil
}

// renameSession names the session and persists it, so saves stop generating a
// title; in a group the member follows, and the returned line says how.
func (m *model) renameSession(name, role string) (string, error) {
	if name != "" {
		m.env.SessionName = name
	}
	note := m.renameMember(name, role)
	return note, m.appendSessionState()
}

// clearSession starts a fresh durable identity after retiring the previous writer.
func (m *model) clearSession() error {
	m.ResetAgentRuntime()
	if m.services.Agent.Active() {
		return fmt.Errorf("agent is still stopping")
	}
	if m.workerMu != nil {
		m.workerMu.Lock()
		defer m.workerMu.Unlock()
	}
	_ = m.services.Session.Close()
	m.services.Session = nil
	m.services.sessionID = session.NewSessionID()
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.env.ApplyModePermissions(m.env.CWD)
	id := m.services.sessionID
	group.BindSession(func() string { return id })
	if m.services.Hook != nil {
		m.services.Hook.SetSession(m.sessionID(), m.sessionTranscriptPath())
	}
	group.Release()
	m.grp = groupState{polling: m.grp.polling}
	m.env.SessionName = ""
	m.earlyNotices = nil
	m.pendingMessages = nil
	m.beforeGoal = nil
	m.env.AutoPilot = autopilotWithoutSessionFields(m.services.Setting.Snapshot().AutoPilot)
	m.rebuildAutopilotReviewer()
	m.setTrackerStorageDir("")
	m.initTaskStorage(m.sessionID())
	m.ReconfigureAgentTool()
	return nil
}
