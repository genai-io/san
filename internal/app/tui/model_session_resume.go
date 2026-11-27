// TUI session resume validation and history restoration.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	apphook "github.com/genai-io/san/internal/app/hook"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/fs"
)

// resumeDirectory reports whether resuming in the original workspace needs a new execution CWD.
func resumeDirectory(history session.Projection, requested string) (bool, error) {
	initial := history.Identity().InitialCWD
	if saved := history.State().ExecutionCWD; saved != nil {
		err := session.ValidateResumeDirectory(*saved, requested)
		if err != nil && initial != "" && session.ValidateResumeDirectory(initial, requested) == nil {
			return true, nil
		}
		return false, err
	}
	return false, session.ValidateResumeDirectory(initial, requested)
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
	if _, err := resumeDirectory(history, m.env.CWD); err != nil {
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
	reuse := opened != nil && opened.ID() == id && opened.Err() == nil
	if !reuse {
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
	}
	history = opened.Snapshot()
	resetCWD, err := resumeDirectory(history, m.env.CWD)
	if err == nil {
		_, _, err = m.resolveSessionModel(history)
	}
	if err == nil && resetCWD {
		err = opened.Append(context.Background(), session.Event{Payload: session.SessionUpdatePayload{State: &session.StatePatch{ExecutionCWD: session.SetField(m.env.CWD)}}})
		history = opened.Snapshot()
	}
	if err != nil {
		if !reuse {
			return errors.Join(err, opened.Close())
		}
		return err
	}
	if reuse && m.services.Hook != nil {
		oldHooks := m.services.Hook
		next := oldHooks.ForSession(opened.ID(), m.env.CWD, opened.Path())
		oldHooks.Shutdown()
		m.services.Hook = next
		apphook.Bind(next, m.env.LLMProvider, opened, m.env.GetModelID())
		m.configureAsyncHookCallback()
		m.wireTaskLifecycle(next)
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
	if m.resumeInstructionsChanged(history) {
		m.addResumeNotice("Resumed with updated model instructions.")
	}
	return nil
}

func (m *model) resumeInstructionsChanged(history session.Projection) bool {
	saved := history.Instructions()
	stable := make(map[string]session.InstructionSection, len(saved))
	for _, section := range saved {
		if section.Slot != int(core.SlotEnvironment) {
			stable[section.Name] = section.InstructionSection
		}
	}
	if len(stable) == 0 {
		return false
	}
	for _, section := range m.promptParams().System().Sections() {
		if section.Slot == core.SlotEnvironment {
			continue
		}
		if text := section.Render(); text != "" {
			if stable[section.Name] != (session.InstructionSection{Content: text, Slot: int(section.Slot)}) {
				return true
			}
			delete(stable, section.Name)
		}
	}
	return len(stable) != 0
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
	apphook.Bind(m.services.Hook, provider, m.services.Session, info.ModelID)
	m.resetRecap()
	m.conv.Messages = m.services.SessionStore.ChatRows(history)
	m.earlyNotices = nil
	active := history.ActiveMessages()
	m.services.Agent.SetMessages(active)
	m.services.Reminder.RestoreHistory(active)
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
	if len(unfinished.Inferences) > 0 {
		m.addResumeNotice(fmt.Sprintf("Resumed with %d unfinished model call(s); no response was committed and no call was retried.", len(unfinished.Inferences)))
	}
	for _, call := range unfinished.NeverRan {
		m.addResumeNotice("Tool call " + call + " did not pass its permission gate; it was not run.")
	}
	for _, call := range unfinished.Unknown {
		m.addResumeNotice("Tool call " + call + " was allowed but has no committed completion; its outcome is unknown and it was not retried.")
	}
	return nil
}

// Resume status is presentation only, not a durable annotation or model input.
func (m *model) addResumeNotice(text string) {
	m.conv.Messages = append(m.conv.Messages, core.ChatMessage{Role: core.ChatNotice, Content: text})
}
