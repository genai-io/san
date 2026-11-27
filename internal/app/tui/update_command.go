// Slash-command execution: builds SlashCommandEnv and runs commands
// through input.NewSlashCommandController.
package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/tui/input"
)

func (m *model) slashCommandEnv() input.SlashCommandEnv {
	var flows input.FlowWorkspace
	if m.features.Flow != nil {
		flows = m.features.Flow
	}
	return input.SlashCommandEnv{
		// UI state
		Input:        &m.userInput,
		Conversation: &m.conv.ConversationModel,
		Tool:         &m.conv.Tool,
		Width:        m.env.Width,
		Height:       m.env.Height,
		Cwd:          m.env.CWD,

		// Services
		Setting:      m.features.Setting,
		LLM:          m.features.LLM,
		SessionStore: m.features.SessionStore,
		Session:      m.features.Session,
		SessionID:    m.sessionID,
		Command:      m.features.Command,
		Skill:        m.features.Skill,
		Plugin:       m.features.Plugin,
		MCP:          m.features.MCP,
		Tracker:      m.features.Tracker,
		Cron:         m.features.Cron,
		ToolSvc:      m.features.Tool,
		TaskSvc:      m.features.Task,
		Flow:         flows,

		// Env callbacks
		GetThinkingEffort: func() string { return m.env.EffectiveThinkingEffort() },
		SetThinkingEffort: m.env.SetThinkingEffort,
		ResetTokens:       m.env.ResetTokens,
		GetGoal:           func() string { return strings.TrimSpace(m.env.AutoPilot.Mission) },
		ContextUsage:      m.contextUsage,
		SubscriptionUsage: m.subscriptionUsageLabel,

		// Model actions
		CommitMessages:          m.CommitMessages,
		SubmitToAgent:           m.SubmitToAgent,
		HandleSkillInvocation:   m.HandleSkillInvocation,
		StartExternalEditor:     m.StartExternalEditor,
		ReloadAfterPluginChange: m.ReloadAfterPluginChange,
		AppendSessionState:      m.appendSessionState,
		ReconfigureAgentTool:    m.ReconfigureAgentTool,
		StopAgentRuntime:        m.StopAgentRuntime,
		ClearSession:            m.clearSession,
		FireSessionEnd:          m.FireSessionEnd,
		BuildCompactRequest:     m.BuildCompactRequest,
		BeginCompact:            func() { m.pendingStopHook = nil },
		SpinnerTickCmd:          m.SpinnerTickCmd,
		ResetCronQueue:          m.ResetCronQueue,
		ForkSession:             m.forkSession,
		SetActivePersona:        m.setActivePersona,
		RenameSession:           m.renameSession,
		GroupCommand:            m.groupCommand,
		TrustWorkspace:          m.trustWorkspace,
		SideQuestion:            m.sideQuestion,
		Recap:                   m.recapNow,
		UpdateMemory:            m.updateMemory,
		OpenHistory:             m.openHistory,
		OpenTasks:               m.openTasks,
		ResetAgentRuntime:       m.ResetAgentRuntime,
		ClearScrollback:         m.clearScrollback,
	}
}

func (m *model) executeCommand(ctx context.Context, inputText string) (string, tea.Cmd, bool) {
	return input.NewSlashCommandController(m.slashCommandEnv()).Execute(ctx, inputText)
}
