// Model lifecycle: construction (newModel/newBaseModel), startup-time
// option application (--continue / --resume / --plugin-dir), plugin-change
// state reload, memory-context priming, task lifecycle wiring, and
// SessionEnd shutdown.
package app

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/hub"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/trigger"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func newModel(opts setting.RunOptions) (*model, error) {
	base := newBaseModel()
	m := &base

	m.agentEventHub.Register("main", func(e hub.Event) { m.mainEvents <- e })

	// Wire task completion: closure captures hub + hooks + tracker directly.
	m.wireTaskLifecycle(m.services.Hook)

	m.configureAsyncHookCallback()
	m.ensureMemoryContextLoaded()
	m.ReconfigureAgentTool()
	m.applyPersonaSkills()
	m.applyPersonaAgents()
	m.wireReminderProviders()
	m.InitTaskStorage()
	m.userInput.Autopilot.SetMissionRefiner(m.missionRefine)
	m.userInput.Autopilot.SetConfigSource(func() setting.AutoPilotSettings { return m.env.AutoPilot })
	if err := m.applyRunOptions(opts); err != nil {
		return nil, err
	}
	return m, nil
}

func newBaseModel() model {
	svc := newServices()
	environment := newEnv(svc.LLM, appCwd, svc.Setting.IsGitRepo(appCwd))
	if settings := svc.Setting.Snapshot(); settings != nil {
		environment.ApplyDefaultPermissionMode(settings.Permissions.DefaultMode, appCwd, svc.Setting.AllowBypass())
		environment.ShowContextBar = settings.ShowContextBar()
		environment.AutoPilot = settings.AutoPilot.Clone()
	}
	return model{
		userInput: input.New(appCwd, defaultWidth, commandSuggestionMatcher(svc.Command), input.SelectorDeps{
			AgentRegistry:   &agentRegistryAdapter{svc.Subagent},
			PersonaRegistry: svc.Persona,
			SkillRegistry:   svc.Skill,
			MCPRegistry:     svc.MCP,
			PluginRegistry:  svc.Plugin,
			Setting:         svc.Setting,
			LoadDisabled:    svc.Setting.GetDisabledToolsAt,
			UpdateDisabled:  svc.Setting.UpdateDisabledToolsAt,
		}),
		conv:                conv.NewModel(defaultWidth),
		agentEventHub:       hub.New(),
		mainEvents:          make(chan hub.Event, 64),
		systemInput:         trigger.New(),
		env:                 environment,
		services:            svc,
		reviewerApprovals:   new(atomic.Int64),
		reviewerEscalations: new(atomic.Int64),
		pendingDecisions:    new(sync.Map),
		autopilot:           new(atomic.Pointer[autopilotRuntime]),
	}
}

func (m *model) applyRunOptions(opts setting.RunOptions) error {
	if opts.PluginDir != "" {
		ctx := context.Background()
		if err := m.services.Plugin.LoadFromPath(ctx, opts.PluginDir); err != nil {
			return fmt.Errorf("failed to load plugins from %s: %w", opts.PluginDir, err)
		}
		if err := m.ReloadAfterPluginChange(); err != nil {
			return err
		}
	}

	if opts.Prompt != "" {
		m.env.InitialPrompt = opts.Prompt
	}

	if opts.Persona != "" {
		if err := m.services.Persona.Validate(opts.Persona); err != nil {
			return err
		}
		if err := m.setActivePersona(opts.Persona); err != nil {
			return err
		}
	}

	if opts.Continue {
		if err := m.applyContinueOption(); err != nil {
			return err
		}
	}

	if opts.Resume {
		if err := m.applyResumeOption(opts.ResumeID); err != nil {
			return err
		}
	}

	return nil
}

// ReloadAfterPluginChange rebuilds the state that plugins contribute to after
// the active plugin set changes — a --plugin-dir load at startup, or a /plugin
// install / uninstall mid-session. It reloads the project's feature services,
// re-merges plugin hooks, and re-wires the agent tool, persona, and reminders
// so the running session reflects the new set.
func (m *model) ReloadAfterPluginChange() error {
	// Plugins were just loaded by the caller; rebuild the project's feature
	// services (not the plugins themselves) and re-point at them.
	m.reloadProjectServices(m.env.CWD)

	plugin.MergePluginHooksIntoSettings(m.services.Setting.Snapshot())
	m.syncSettingsToHookEngine()
	m.ReconfigureAgentTool()
	m.applyPersonaSkills()
	m.applyPersonaAgents()

	// Refresh skills/memory reminders so the LLM sees the updated skill set
	// in the next user message instead of waiting for SessionStart/PostCompact.
	m.services.Reminder.RequeueSystemReminders()

	return nil
}

func (m *model) applyContinueOption() error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return fmt.Errorf("failed to initialize session store: %w", err)
	}

	sess, err := m.services.Session.LoadLatest()
	if err != nil {
		return fmt.Errorf("no previous session to continue: %w", err)
	}

	m.restoreSessionData(sess)
	return nil
}

func (m *model) applyResumeOption(resumeID string) error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return fmt.Errorf("failed to initialize session store: %w", err)
	}

	if resumeID != "" {
		sess, err := m.services.Session.Load(resumeID)
		if err != nil {
			return fmt.Errorf("failed to load session %s: %w", resumeID, err)
		}
		m.restoreSessionData(sess)
		return nil
	}

	m.userInput.Session.PendingSelector = true
	return nil
}

func (m *model) ensureMemoryContextLoaded() {
	if m.env.CachedUserInstructions != "" || m.env.CachedProjectInstructions != "" {
		return
	}
	m.refreshMemoryContext(m.env.CWD, "session_start")
}

func (m *model) wireTaskLifecycle(hookEngine hook.Handler) {
	trackerSvc := m.services.Tracker
	agentEventHub := m.agentEventHub

	fireHook := func(event hook.EventType, info task.TaskInfo) {
		if hookEngine == nil {
			return
		}
		subject := hub.TaskSubject(info)
		hookEngine.ExecuteAsync(event, hook.HookInput{
			TaskID:          info.ID,
			TaskSubject:     subject,
			TaskDescription: info.Description,
		})
	}

	task.SetLifecycleHandler(taskLifecycleFunc{
		onCreated: func(info task.TaskInfo) {
			fireHook(hook.TaskCreated, info)
		},
		onCompleted: func(info task.TaskInfo) {
			fireHook(hook.TaskCompleted, info)
			todo.CompleteWorker(trackerSvc, info)

			subject := hub.TaskSubject(info)
			msg, ok := hub.TaskMessage(info, subject)
			if !ok {
				return
			}
			agentEventHub.Publish(hub.Event{
				Type:    "task.completed",
				Target:  "main",
				Subject: msg.Notice,
				Data:    msg.Content,
			})
		},
	})
}

type taskLifecycleFunc struct {
	onCreated   func(task.TaskInfo)
	onCompleted func(task.TaskInfo)
}

func (f taskLifecycleFunc) TaskCreated(info task.TaskInfo)   { f.onCreated(info) }
func (f taskLifecycleFunc) TaskCompleted(info task.TaskInfo) { f.onCompleted(info) }

func (m *model) FireSessionEnd(reason string) {
	m.services.Hook.Execute(context.Background(), hook.SessionEnd, hook.HookInput{
		Reason: reason,
	})
	m.services.Hook.ClearSessionHooks()
	if m.systemInput.FileWatcher != nil {
		m.systemInput.FileWatcher.Stop()
	}
}
