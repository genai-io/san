// Model lifecycle: construction (newModel/newBaseModel), startup-time
// option application (--continue / --resume / --plugin-dir), plugin-backed
// state reload, memory-context priming, task lifecycle wiring, and
// SessionEnd shutdown.
package app

import (
	"context"
	"fmt"

	"github.com/genai-io/gen-code/internal/app/conv"
	"github.com/genai-io/gen-code/internal/app/hub"
	"github.com/genai-io/gen-code/internal/app/input"
	"github.com/genai-io/gen-code/internal/app/trigger"
	"github.com/genai-io/gen-code/internal/command"
	"github.com/genai-io/gen-code/internal/hook"
	"github.com/genai-io/gen-code/internal/mcp"
	"github.com/genai-io/gen-code/internal/plugin"
	"github.com/genai-io/gen-code/internal/setting"
	"github.com/genai-io/gen-code/internal/skill"
	"github.com/genai-io/gen-code/internal/subagent"
	"github.com/genai-io/gen-code/internal/task"
	"github.com/genai-io/gen-code/internal/task/tracker"
)

func newModel(opts setting.RunOptions) (*model, error) {
	base := newBaseModel()
	m := &base

	m.agentEventHub.Register("main", func(e hub.Event) { m.mainEvents <- e })

	// Wire task completion: closure captures hub + hooks + tracker directly.
	var hookEngine *hook.Engine
	if m.services.Hook != nil {
		hookEngine = m.services.Hook
	}
	m.wireTaskLifecycle(hookEngine)

	m.configureAsyncHookCallback()
	m.ensureMemoryContextLoaded()
	m.ReconfigureAgentTool()
	m.wireReminderProviders()
	m.InitTaskStorage()
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
	}
	return model{
		userInput: input.New(appCwd, defaultWidth, commandSuggestionMatcher(svc.Command), input.SelectorDeps{
			AgentRegistry:    &agentRegistryAdapter{svc.Subagent},
			SkillRegistry:    svc.Skill,
			MCPRegistry:      svc.MCP,
			PluginRegistry:   svc.Plugin,
			IdentityRegistry: svc.Identity,
			Setting:          svc.Setting,
			LoadDisabled:     svc.Setting.GetDisabledToolsAt,
			UpdateDisabled:   svc.Setting.UpdateDisabledToolsAt,
		}),
		conv:          conv.NewModel(defaultWidth),
		agentEventHub: hub.New(),
		mainEvents:    make(chan hub.Event, 64),
		systemInput:   trigger.New(),
		env:           environment,
		services:      svc,
	}
}

func (m *model) applyRunOptions(opts setting.RunOptions) error {
	if opts.PluginDir != "" {
		ctx := context.Background()
		if err := m.services.Plugin.LoadFromPath(ctx, opts.PluginDir); err != nil {
			return fmt.Errorf("failed to load plugins from %s: %w", opts.PluginDir, err)
		}
		if err := m.ReloadPluginBackedState(); err != nil {
			return err
		}
	}

	if opts.Prompt != "" {
		m.env.InitialPrompt = opts.Prompt
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

func (m *model) ReloadPluginBackedState() error {
	skill.Initialize(skill.Options{CWD: m.env.CWD})
	command.Initialize(command.Options{
		CWD:                m.env.CWD,
		DynamicProviders:   []func() []command.Info{skillCommandInfos},
		PluginCommandPaths: pluginCommandPaths,
	})
	subagent.Initialize(subagent.Options{CWD: m.env.CWD, PluginAgentPaths: pluginAgentPaths})
	mcp.Initialize(mcp.Options{CWD: m.env.CWD, PluginServers: pluginMCPServers})
	setting.Initialize(setting.Options{CWD: m.env.CWD})

	m.services.refreshAfterReload()
	m.userInput.Identity.SetRegistry(m.services.Identity)

	if m.services.Hook != nil {
		plugin.MergePluginHooksIntoSettings(m.services.Setting.Snapshot())
	}
	m.syncSettingsToHookEngine()
	m.ReconfigureAgentTool()

	// Refresh skills/memory reminders so the LLM sees the updated skill set
	// in the next user message instead of waiting for SessionStart/PostCompact.
	if m.services.Reminder != nil {
		m.services.Reminder.EnqueueAllProviders()
	}

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
			tracker.CompleteWorker(trackerSvc, info)

			subject := hub.TaskSubject(info)
			msg, ok := hub.TaskMessage(info, subject)
			if !ok {
				return
			}
			agentEventHub.Publish(hub.Event{
				Type:    "task.completed",
				Source:  fmt.Sprintf("agent:%s", info.ID),
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
	if m.services.Hook != nil {
		m.services.Hook.Execute(context.Background(), hook.SessionEnd, hook.HookInput{
			Reason: reason,
		})
		m.services.Hook.ClearSessionHooks()
	}
	if m.systemInput.FileWatcher != nil {
		m.systemInput.FileWatcher.Stop()
	}
}
