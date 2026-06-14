// Session persistence and per-session task storage.
// Save/load conversations + task snapshots to disk, wire the task tracker's
// storage directory, fork a fresh session from the current one.
package app

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
)

func (m *model) InitTaskStorage() {
	m.initTaskStorage(m.services.Session.ID())
}

func (m *model) PersistSession() error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return err
	}
	if len(m.conv.Messages) == 0 {
		return nil
	}

	entries := session.ConvertToEntries(m.conv.Messages)

	var providerName, modelID string
	if m.env.CurrentModel != nil {
		providerName = string(m.env.CurrentModel.Provider)
		modelID = m.env.CurrentModel.ModelID
	}

	sess := &session.Snapshot{
		Metadata: session.SessionMetadata{
			ID:         m.services.Session.ID(),
			Provider:   providerName,
			Model:      modelID,
			Cwd:        m.env.CWD,
			LastPrompt: session.ExtractLastUserText(entries),
			Mode:       m.env.SessionMode(),
		},
		Entries:           entries,
		Tasks:             m.services.Tracker.Export(),
		OmitMessageWrites: m.services.Session.Recorder() != nil,
	}

	if sess.Metadata.Title == "" || sess.Metadata.ID == "" {
		sess.Metadata.Title = session.GenerateTitle(sess.Entries)
	}

	if err := m.services.Session.Save(sess); err != nil {
		return err
	}

	m.services.Session.SetID(sess.Metadata.ID)
	m.initTaskStorage(m.services.Session.ID())

	if m.services.Hook != nil {
		m.services.Hook.SetTranscriptPath(m.services.Session.GetStore().SessionPath(sess.Metadata.ID))
	}
	m.ReconfigureAgentTool()

	return nil
}

type persistSessionDoneMsg struct{ err error }

// Only safe when the session ID is already established (i.e. not the first save).
func (m *model) persistSessionCmd() tea.Cmd {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		log.Logger().Warn("failed to ensure session store for async persist", zap.Error(err))
		return nil
	}
	if len(m.conv.Messages) == 0 {
		return nil
	}

	entries := session.ConvertToEntries(m.conv.Messages)

	var providerName, modelID string
	if m.env.CurrentModel != nil {
		providerName = string(m.env.CurrentModel.Provider)
		modelID = m.env.CurrentModel.ModelID
	}

	sess := &session.Snapshot{
		Metadata: session.SessionMetadata{
			ID:         m.services.Session.ID(),
			Provider:   providerName,
			Model:      modelID,
			Cwd:        m.env.CWD,
			LastPrompt: session.ExtractLastUserText(entries),
			Mode:       m.env.SessionMode(),
		},
		Entries:           entries,
		Tasks:             m.services.Tracker.Export(),
		OmitMessageWrites: m.services.Session.Recorder() != nil,
	}

	if sess.Metadata.Title == "" {
		sess.Metadata.Title = session.GenerateTitle(sess.Entries)
	}

	store := m.services.Session.GetStore()
	return func() tea.Msg {
		if store == nil {
			return persistSessionDoneMsg{err: fmt.Errorf("no session store")}
		}
		return persistSessionDoneMsg{err: store.Save(sess)}
	}
}

func (m *model) loadSessionByID(id string) error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return err
	}

	sess, err := m.services.Session.Load(id)
	if err != nil {
		return err
	}

	m.services.Tracker.SetStorageDir("")
	m.restoreSessionData(sess)

	if len(sess.Tasks) == 0 {
		m.services.Tracker.Reset()
	}

	m.env.InputTokens = 0
	m.env.OutputTokens = 0

	return nil
}

func (m *model) restoreSessionData(sess *session.Snapshot) {
	m.conv.Messages = session.ConvertFromEntries(sess.Entries)
	m.services.Session.SetID(sess.Metadata.ID)

	m.initTaskStorage(m.services.Session.ID())

	if len(sess.Tasks) > 0 {
		m.services.Tracker.Import(sess.Tasks)
	}
}

func (m *model) initTaskStorage(sessionID string) {
	if m.services.Tracker.GetStorageDir() != "" {
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
		m.services.Tracker.SetStorageDir(dir)
		_ = m.services.Task.SetOutputDir(filepath.Join(dir, "outputs"))
		return
	}

	if sessionID == "" {
		return
	}
	dir := filepath.Join(confdir.Dir(homeDir), "tasks", sessionID)
	m.services.Tracker.SetStorageDir(dir)
	_ = m.services.Task.SetOutputDir(filepath.Join(dir, "outputs"))
}

func (m *model) forkSession() (string, error) {
	if m.services.Session.ID() == "" {
		return "", fmt.Errorf("no active session to fork")
	}
	forked, err := m.services.Session.Fork(m.services.Session.ID())
	if err != nil {
		return "", err
	}
	originalID := forked.Metadata.ParentSessionID
	m.services.Session.SetID(forked.Metadata.ID)
	m.services.Tracker.SetStorageDir("")
	return originalID, nil
}
