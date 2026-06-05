// Submit dispatch — what happens when the user presses Enter, plus the
// single SubmitToAgent exit point that every input path funnels through.
// Lives entirely on *model so a reader can follow Enter → agent without
// jumping packages.
package app

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/v2/internal/app/input"
	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/log"
)

// handleSubmit is the Enter handler. Reads the textarea; if a turn is
// already streaming, queues for later; otherwise runs the submission
// pipeline.
func (m *model) handleSubmit() tea.Cmd {
	m.userInput.PromptSuggestion.Clear()

	raw := strings.TrimSpace(m.userInput.FullValue())
	if raw == "" && len(m.userInput.Images.Pending) == 0 {
		return nil
	}

	if m.conv.Stream.Active {
		log.QueueLog("handleSubmit: stream active, enqueue %q", raw)
		return m.enqueueWhileStreaming(raw)
	}

	// A manual /compact computes its summary asynchronously, then applies it
	// in place. Accepting a message in that window would race the compaction
	// (the summary predates the new message, so an in-place replace could drop
	// it). Hold submission until compaction finishes; the textarea keeps the
	// text so the user just presses Enter again.
	if m.conv.Compact.Active {
		m.conv.AddNotice("Compaction in progress — please resubmit in a moment.")
		return nil
	}

	log.QueueLog("handleSubmit: stream idle, normal submit %q", raw)
	m.conv.Compact.ClearResult()
	return m.dispatchSubmission(raw)
}

// enqueueWhileStreaming parks the input in the queue and resets the
// textarea. The queued item is dequeued either by drainInputQueueAfterCancel
// (on stream cancel) or by drainTurnQueues (at turn boundary).
func (m *model) enqueueWhileStreaming(raw string) tea.Cmd {
	images := m.userInput.PendingImages()
	if m.userInput.Queue.Enqueue(raw, images) < 0 {
		m.conv.AddNotice("Input queue is full. Please wait for the current turn to complete.")
		return nil
	}
	m.userInput.Reset()
	log.QueueLog("enqueueWhileStreaming: queued %q queueLen=%d", raw, m.userInput.Queue.Len())
	return nil
}

// dispatchSubmission runs the submission pipeline: exit shortcut →
// prompt-hook gate → history → slash command? → otherwise send to
// agent. Shared by the Enter handler and drainInputQueueAfterCancel.
func (m *model) dispatchSubmission(raw string) tea.Cmd {
	if input.IsExitRequest(raw) {
		cmd, _ := m.QuitWithCancel()
		return cmd
	}

	if blocked, reason := m.checkPromptHook(context.Background(), raw); blocked {
		m.conv.AddNotice("Prompt blocked: " + reason)
		m.userInput.Reset()
		return tea.Batch(m.CommitMessages()...)
	}

	m.userInput.RecordSubmission(m.env.CWD, raw)

	if cmd, handled := m.runSlashCommandIfMatched(raw); handled {
		return cmd
	}

	msg, ok := m.buildUserMessage(raw)
	if !ok {
		return tea.Batch(m.CommitMessages()...)
	}
	m.conv.Append(msg)
	m.userInput.Reset()
	return m.SubmitToAgent(msg.Content, msg.Images)
}

// runSlashCommandIfMatched returns (cmd, true) if `raw` is a slash command
// the controller handled, or (nil, false) if it should fall through to a
// provider turn.
func (m *model) runSlashCommandIfMatched(raw string) (tea.Cmd, bool) {
	ctrl := input.NewSlashCommandController(m.slashCommandEnv())
	return ctrl.HandleSubmit(raw)
}

// buildUserMessage resolves image references in raw text into a ChatMessage
// ready to append. Returns ok=false if image resolution failed (in which
// case a notice has already been appended to conv).
func (m *model) buildUserMessage(raw string) (core.ChatMessage, bool) {
	content, fileImages, err := input.ProcessImageRefs(m.env.CWD, raw)
	if err != nil {
		m.conv.AddNotice("Image error: " + err.Error())
		return core.ChatMessage{}, false
	}
	displayContent := content
	content, inlineImages := m.userInput.ExtractInlineImages(content)
	allImages := make([]core.Image, 0, len(inlineImages)+len(fileImages))
	allImages = append(allImages, inlineImages...)
	allImages = append(allImages, fileImages...)
	return core.ChatMessage{
		Role:           core.RoleUser,
		Content:        content,
		DisplayContent: displayContent,
		Images:         allImages,
	}, true
}

// drainInputQueueAfterCancel pops one queued item (if any) and runs it
// through the normal submission pipeline. Called from handleStreamCancel
// so a user who queued input while a turn was streaming sees the next one
// run after Ctrl+C.
func (m *model) drainInputQueueAfterCancel() tea.Cmd {
	item, ok := m.userInput.Queue.Dequeue()
	if !ok {
		return nil
	}
	m.conv.Compact.ClearResult()
	m.userInput.RestoreImages(item.Images)
	return m.dispatchSubmission(item.Content)
}

// SubmitToAgent is the single exit point for "send this content to the
// agent" — user Enter, slash command output, skill button, cron fire,
// hook continuation, hub notification. Ensures the agent session is up,
// pushes content+images onto its inbox, returns the outbox-poll cmd.
// On no-provider or ensureAgentSession failure, posts a notice and
// returns a commit cmd (the agent is not contacted).
func (m *model) SubmitToAgent(content string, images []core.Image) tea.Cmd {
	log.QueueLog("SubmitToAgent: %q", truncate(content, 60))
	if m.env.LLMProvider == nil {
		return m.notifyNoProvider()
	}

	startCmd, err := m.ensureAgentSession(content)
	if err != nil {
		m.conv.AddNotice("Failed to start agent: " + err.Error())
		return tea.Batch(m.CommitMessages()...)
	}

	m.env.DetectThinkingKeywords(content)

	sendCmd := m.sendToAgent(content, images)
	if startCmd != nil {
		return tea.Batch(startCmd, sendCmd)
	}
	return sendCmd
}

// notifyNoProvider posts the standard "no provider connected" notice
// and returns a commit cmd.
func (m *model) notifyNoProvider() tea.Cmd {
	m.conv.AddNotice(input.NoProviderMsg)
	return tea.Batch(m.CommitMessages()...)
}

// HandleSkillInvocation runs the agent against the pending skill
// invocation: consume → append to conv → SubmitToAgent. Plugin root
// (if the skill came from a plugin) is set on the agent so hooks/tools
// fired during the turn see PLUGIN_ROOT pointing at that plugin.
func (m *model) HandleSkillInvocation() tea.Cmd {
	displayMsg, fullMsg, pluginRoot := m.userInput.Skill.ConsumeInvocation()
	if m.env.LLMProvider == nil {
		return m.notifyNoProvider()
	}
	m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: fullMsg, DisplayContent: displayMsg})
	if pluginRoot != "" {
		m.services.Agent.SetPluginRoot(pluginRoot)
	}
	return m.SubmitToAgent(fullMsg, nil)
}
