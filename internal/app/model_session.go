// Session persistence and per-session task storage.
// Save/load conversations + task snapshots to disk, wire the task tracker's
// storage directory, fork a fresh session from the current one.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/fs"
)

func (m *model) PersistSession() error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return err
	}
	sess := m.buildSessionSnapshot()
	if sess == nil {
		return nil
	}

	if err := m.services.Session.Save(sess); err != nil {
		return err
	}

	m.adoptSession(sess.Metadata.ID)
	m.initTaskStorage(m.services.Session.ID())
	m.ReconfigureAgentTool()

	return nil
}

// adoptSession makes id the current session everywhere that carries it: the
// session service and the hook engine, whose session_id and transcript_path
// would otherwise stay on the id minted at startup after a resume or fork.
func (m *model) adoptSession(id string) {
	m.services.Session.SetID(id)
	m.services.Hook.SetSession(id, m.services.Session.TranscriptPath())
}

type persistSessionDoneMsg struct{ err error }

// Only safe when the session ID is already established (i.e. not the first save).
func (m *model) persistSessionCmd() tea.Cmd {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		log.Logger().Warn("failed to ensure session store for async persist", zap.Error(err))
		return nil
	}
	sess := m.buildSessionSnapshot()
	if sess == nil {
		return nil
	}

	store := m.services.Session.GetStore()
	return func() tea.Msg {
		if store == nil {
			return persistSessionDoneMsg{err: fmt.Errorf("no session store")}
		}
		return persistSessionDoneMsg{err: store.Save(sess)}
	}
}

// persistAfterTurn saves the session at the end of a turn, choosing the
// strategy by session state: the first save (no ID yet) runs synchronously so
// the session ID, task storage, and transcript wiring are established before
// the next turn; later saves go through the async command. Returns the async
// command to batch, or nil when the synchronous path ran (it logs its own
// error, matching how async failures surface via persistSessionDoneMsg).
func (m *model) persistAfterTurn() tea.Cmd {
	if m.services.Session.ID() != "" {
		return m.persistSessionCmd()
	}
	if err := m.PersistSession(); err != nil {
		log.Logger().Warn("failed to persist session at turn end", zap.Error(err))
	}
	return nil
}

// buildSessionSnapshot assembles the current conversation + task state into a
// session.Snapshot ready to Save, or nil when there is nothing to persist (no
// messages). Shared by the synchronous PersistSession and the async
// persistSessionCmd so the snapshot stays identical across both paths.
func (m *model) buildSessionSnapshot() *session.Snapshot {
	if len(m.conv.Messages) == 0 {
		return nil
	}

	msgs := m.conv.Messages

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
			LastPrompt: session.ExtractLastUserText(msgs),
			Mode:       m.env.SessionMode(),
			AutoPilot:  marshalAutoPilot(m.env.AutoPilot),
		},
		Messages:          msgs,
		Tasks:             m.services.Tracker.Export(),
		OmitMessageWrites: m.services.Session.Recorder() != nil,
	}

	if sess.Metadata.Title == "" {
		if m.env.SessionName != "" {
			sess.Metadata.Title = m.env.SessionName
		} else {
			sess.Metadata.Title = session.GenerateTitle(sess.Messages)
		}
	}

	return sess
}

func (m *model) loadSessionByID(id string) error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return err
	}

	sess, err := m.services.Session.Load(id)
	if err != nil {
		return err
	}

	// This is a different persisted conversation. Do not let a stopped agent's
	// restart snapshot override the selected transcript on the next turn, and
	// don't let the previous conversation's Reads satisfy Edit's
	// read-before-modify gate in this one.
	m.ResetAgentSession()
	fs.ResetFileViews()
	m.restoreSessionData(sess)

	if len(sess.Tasks) == 0 {
		m.services.Tracker.Reset()
	}

	m.env.ResetContextDisplay()

	return nil
}

func (m *model) restoreSessionData(sess *session.Snapshot) {
	m.conv.Messages = sess.Messages
	m.systemRemindersSent = slices.ContainsFunc(sess.Messages, func(msg core.ChatMessage) bool {
		return msg.Role == core.ChatUser && reminder.HasSystemReminder(msg.Content)
	})
	m.applyResumeWindow(m.services.Setting.Snapshot().ResumeWindowMessageCount())
	m.adoptSession(sess.Metadata.ID)
	m.env.SessionName = sess.Metadata.Title

	// Leave any earlier session's tracker directory behind: initTaskStorage
	// keeps a directory that is already set, and this session's items are
	// re-imported below after SetStorageDir's disk load.
	m.setTrackerStorageDir("")
	m.initTaskStorage(m.services.Session.ID())

	if len(sess.Tasks) > 0 {
		m.services.Tracker.Import(sess.Tasks)
	}

	// Restore the session's autopilot config; an empty blob (e.g. an older
	// session) leaves the settings-seeded default in place. Rebuild the runtime
	// snapshot so a mid-session /resume (the agent may already be running) takes
	// the resumed steers/model/permission config immediately, not on the next
	// /autopilot Save.
	if ar := parseAutoPilot(sess.Metadata.AutoPilot); !ar.IsZero() {
		// A resumed run waits for the human instead of driving on its own.
		if ar.MissionState == setting.MissionRunning {
			ar.MissionState = setting.MissionPaused
		}
		m.env.AutoPilot = ar
	}
	m.rebuildAutopilotReviewer()

	// Resume into the operation mode the session was saved in, so an autopilot
	// run picks up where it left off without re-cycling shift+tab. (Bypass never
	// round-trips — parseSessionMode maps it to Normal.) A session that saved no
	// mode keeps the launch's — see resumeOperationMode.
	if mode := resumeOperationMode(sess.Metadata.Mode, m.env.OperationMode); mode != m.env.OperationMode {
		m.env.OperationMode = mode
		m.applyOperationMode()
	}
}

// applyResumeWindow marks everything before the last tail messages as already
// committed, so a resume prints only the window. The skipped messages stay in
// conv.Messages for /history and the next save. A notice opening the window
// says how many were skipped; like every notice it is never saved or sent to
// the model.
func (m *model) applyResumeWindow(tail int) {
	start := resumeWindowStart(m.conv.Messages, tail)
	m.conv.CommittedCount = start
	m.conv.ResumeWindowStart = start
	if start > 0 {
		m.conv.Messages = slices.Insert(m.conv.Messages, start, core.ChatMessage{
			Role:    core.ChatNotice,
			Content: fmt.Sprintf("… %s earlier not shown · /history to read them", kit.Plural(start, "message")),
		})
	}
}

// resumeWindowStart returns the first message index a resumed session replays:
// the start of the last `tail` messages, snapped back so the window never opens
// on a tool result whose owning assistant falls outside it. A non-positive tail
// replays nothing.
//
// The snap is load-bearing, not cosmetic: PrecomputeInlinedResults only pairs a
// result with an assistant at or after the window start (conv/view.go), so a
// window opening mid-turn would render that result standalone *and* inline
// under its assistant — the same content twice. An assistant always commits
// together with its tool results.
func resumeWindowStart(messages []core.ChatMessage, tail int) int {
	if tail <= 0 {
		return len(messages)
	}
	if len(messages) <= tail {
		return 0
	}
	start := len(messages) - tail
	for start > 0 && messages[start].ToolResult != nil {
		start--
	}
	return start
}

func (m *model) initTaskStorage(sessionID string) {
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
	if m.services.Session.ID() == "" {
		return "", fmt.Errorf("no active session to fork")
	}
	forked, err := m.services.Session.Fork(m.services.Session.ID())
	if err != nil {
		return "", err
	}
	originalID := forked.Metadata.ParentSessionID
	m.adoptSession(forked.Metadata.ID)
	// The live agent holds an onEvent closure over a Recorder bound to the
	// parent's id, and a Recorder's session is fixed at construction. Without
	// this stop it keeps writing the fork's messages, inferences, permissions
	// and hooks into the parent transcript — so resuming the original, which is
	// exactly what the UI tells the user to do, replays post-fork history and
	// the parent is no longer frozen at the fork point.
	//
	// Stop rather than Reset: a fork continues the conversation under a new id,
	// so the chain has to survive into the rebuilt agent. The next turn's
	// ensureAgentSession rebuilds with a Recorder bound to the new id, since
	// NewRecorder invalidates its cache on exactly that mismatch.
	m.StopAgentSession()
	// Same storage switch as loadSessionByID: clear the tracker so
	// initTaskStorage adopts the fork's directory, then re-import the tasks
	// that SetStorageDir's disk load replaced.
	m.setTrackerStorageDir("")
	m.initTaskStorage(forked.Metadata.ID)
	m.services.Tracker.Import(forked.Tasks)
	return originalID, nil
}

// renameSession sets a custom name for the current session and persists it.
// The name is stored in env so subsequent saves use it instead of auto-generating
// a title from the conversation entries.
func (m *model) renameSession(name string) error {
	m.env.SessionName = name
	return m.PersistSession()
}
