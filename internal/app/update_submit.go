// Submit dispatch — what happens when the user presses Enter, plus the
// single SubmitToAgent exit point that every input path funnels through.
// Lives entirely on *model so a reader can follow Enter → agent without
// jumping packages.
package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/image"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
)

// handleSubmit is the Enter handler. Reads the textarea; if a turn is
// already streaming or compacting, queues for later; otherwise runs the submission
// pipeline.
func (m *model) handleSubmitScoped(recording *session.Completion) tea.Cmd {
	if m.conv.Stream.Active || m.conv.Compact.Active || !m.pendingMessages.CanDeliver(false) || m.pendingMessages.Len() != 0 {
		closeAutopilot(recording)
		m.autopilotContinuing = false
		m.userInput.Reset()
		return nil
	}
	raw := strings.TrimSpace(m.userInput.FullValue())
	if raw == "" {
		closeAutopilot(recording)
		return nil
	}
	m.userInput.PromptSuggestion.Clear()
	m.conv.Compact.ClearResult()
	return m.dispatchSubmissionScoped(raw, recording)
}

func (m *model) handleSubmit() tea.Cmd {
	m.userInput.PromptSuggestion.Clear()

	raw := strings.TrimSpace(m.userInput.FullValue())
	if raw == "" && len(m.userInput.Images.Pending) == 0 {
		return nil
	}
	// The person is back: the unattended count restarts, and messages this
	// member was holding ride on this input.
	m.grp.unattended, m.grp.userTyped = 0, true

	if m.conv.Stream.Active {
		// A workflow may keep running while the parent model streams. Its stop
		// command must reach the task now, not after that model turn ends.
		// Side commands, including group control, run outside the main turn.
		if input.IsWorkflowStopCommand(raw) || input.IsSideCommand(raw) {
			m.userInput.RecordSubmission(m.env.CWD, raw)
			result, cmd, _ := m.executeCommand(context.Background(), raw)
			m.userInput.Reset()
			if result == "" {
				return cmd
			}
			// Show the stop result even while the parent inference continues.
			return tea.Batch(cmd, m.deliverNotice(mainNotice{Display: result}))
		}
		log.Logger().Sugar().Debugf("handleSubmit: stream active, enqueue %q", raw)
		return m.enqueueInput(raw)
	}

	// Keep input outside the history being summarized until its replacement is applied.
	if m.conv.Compact.Active {
		return m.enqueueInput(raw)
	}

	log.Logger().Sugar().Debugf("handleSubmit: stream idle, normal submit %q", raw)
	m.conv.Compact.ClearResult()
	return m.dispatchSubmission(raw)
}

// enqueueInput parks input and resets the textarea. Safe boundaries and edit
// exit drain it automatically unless cancellation holds it for explicit input.
func (m *model) enqueueInput(raw string) tea.Cmd {
	images := m.userInput.PendingImages()
	if m.userInput.Queue.Enqueue(raw, images) < 0 {
		m.conv.AddNotice("Input queue is full. Please wait for the current turn to complete.")
		return nil
	}
	m.userInput.Reset()
	log.Logger().Sugar().Debugf("enqueueInput: queued %q queueLen=%d", raw, m.userInput.Queue.Len())
	return nil
}

// dispatchSubmission runs the submission pipeline: exit shortcut →
// prompt-hook gate → history → slash command? → otherwise send to
// agent. Shared by the Enter handler and sendNextQueuedInputIfIdle.
func (m *model) dispatchSubmission(raw string) tea.Cmd {
	return m.dispatchSubmissionScoped(raw, nil)
}

func (m *model) dispatchSubmissionScoped(raw string, recording *session.Completion) tea.Cmd {
	defer func() { closeAutopilot(recording) }()
	if input.IsExitRequest(raw) {
		cmd, _ := m.QuitWithCancel()
		return cmd
	}

	// Capture the copilot's note now, before the flag resets, so the built
	// message can wear its "⎿ autopilot · N/M" continuation annotation.
	autopilotNote := ""
	if m.autopilotContinuing {
		autopilotNote = m.continuationCount()
		m.autopilotContinuing = false
	}

	if blocked, reason := m.checkPromptHook(context.Background(), raw); blocked {
		m.conv.AddNotice("Prompt blocked: " + reason)
		m.userInput.Reset()
		return tea.Batch(m.CommitMessages()...)
	}

	m.userInput.RecordSubmission(m.env.CWD, raw)

	if m.isSlashCommand(raw) {
		if cmd, handled := m.runSlashCommandIfMatched(raw); handled {
			return cmd
		}
	}

	// The human's own message is their turn, not the mission's: a running
	// mission pauses and waits for them to resume it.
	if autopilotNote == "" {
		m.pauseMission()
	}

	msg, ok := m.buildUserMessage(raw)
	if !ok {
		return tea.Batch(m.CommitMessages()...)
	}
	// Text-only models can't receive image parts, so inline each image's path
	// into the content and let the model decide how to use it (e.g. call an
	// MCP tool to inspect the file) instead of refusing the turn. The appended
	// message keeps its images; only inference input drops them.
	msg.Content = m.describeImages(msg.Content, msg.Images)
	msg.AutopilotNote = autopilotNote
	msg = m.conv.Append(msg)
	m.userInput.Reset()
	message, _ := msg.ToMessage()
	p := pendingMessage{Message: message, FromUser: true, Recording: recording}
	recording = nil
	return m.submit(p)
}

// isSlashCommand excludes leading image paths, which also start with "/".
func (m *model) isSlashCommand(raw string) bool {
	_, _, isCmd := command.ParseCommand(raw)
	return isCmd && input.LeadingImagePath(m.env.CWD, raw) == ""
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
	content, inlineImages := m.userInput.ExtractInlineImages(content)
	displayContent, allImages := input.ImageDisplay(m.env.CWD, raw, inlineImages, fileImages)
	return core.ChatMessage{
		Role:           core.ChatUser,
		Content:        content,
		DisplayContent: displayContent,
		Images:         allImages,
	}, true
}

// describeImages keeps an inspectable source path alongside each attachment,
// including for vision models: the image block disappears after compaction, but
// the path in the text survives so the model can read the file again. Pasted
// images live in the session blob dir for as long as the transcript can be resumed.
func (m *model) describeImages(content string, images []core.Attachment) string {
	if len(images) == 0 {
		return content
	}
	vision := llm.SupportsImages(m.env.LLMProvider, m.env.GetModelID())
	var sb strings.Builder
	if content != "" {
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}
	if !vision {
		sb.WriteString("[Attached image(s) — this model cannot view images directly. The files are on disk; use an available tool (e.g. an MCP image-description tool) to inspect them if you need their contents.]\n")
	}
	for i, img := range images {
		path := img.Path
		if path == "" && m.ensureSessionStore() == nil {
			// Persist clipboard bytes instead of pointing the model at a temp
			// path that will vanish when this process exits.
			path, _ = image.EnsureFilePath(img, m.services.SessionStore.ImagesDir(m.sessionID()))
		}
		if path == "" {
			// Never claim a filename is a readable path when writing failed.
			path = "file unavailable"
		}
		fmt.Fprintf(&sb, "[Image #%d: %s]\n", i+1, path)
	}
	return strings.TrimSpace(sb.String())
}

// sendNextQueuedInputIfIdle dispatches queued commands and at most one message,
// preserving the composer draft. A head item under edit waits until editing ends.
func (m *model) sendNextQueuedInputIfIdle() tea.Cmd {
	return m.sendNextQueuedInput(false)
}

// Only queue-selection Enter may bypass the manual-stop hold, never the fence.
func (m *model) sendNextQueuedInput(explicit bool) tea.Cmd {
	cmd, _ := m.sendQueuedInputs(explicit)
	return cmd
}

// sendQueuedInputs reports whether a turn or UI/session transition supersedes
// idle completion. Synchronous local commands alone must not strand the queue.
func (m *model) sendQueuedInputs(explicit bool) (tea.Cmd, bool) {
	var cmds []tea.Cmd
	for m.userInput.Queue.Len() > 0 {
		if !m.pendingMessages.CanDeliver(explicit) || m.conv.Compact.Active || m.conv.Stream.Active || m.userInput.Queue.SelectIdx == 0 {
			break
		}
		if _, active := m.activeOverlay(); active {
			return tea.Batch(cmds...), true
		}
		item, _ := m.userInput.Queue.Dequeue()
		isCommand := m.isSlashCommand(item.Content)
		name, _, _ := command.ParseCommand(item.Content)
		previousSession, previousOutbox := m.services.Session, m.AgentOutbox()
		cmds = append(cmds, m.dispatchQueuedInput(item))
		explicit = false
		if !isCommand || name == "exit" || name == "quit" ||
			m.services.Session != previousSession || m.AgentOutbox() != previousOutbox ||
			m.conv.Stream.Active || m.conv.Compact.Active || m.pendingMessages.Len() > 0 {
			return tea.Batch(cmds...), true
		}
		if _, active := m.activeOverlay(); active {
			return tea.Batch(cmds...), true
		}
	}
	return tea.Batch(cmds...), false
}

func (m *model) dispatchQueuedInput(item input.QueueItem) tea.Cmd {
	m.conv.Compact.ClearResult()

	// The textarea may hold an unrelated draft (in-progress typing on the
	// cancel path, the restored pre-edit stash on the edit-exit path) —
	// dispatchSubmission resets the textarea after a send, so park the draft
	// and put it back.
	draft := m.userInput.Textarea.Value()
	draftImages := slices.Clone(m.userInput.Images.Pending)
	draftNextID := m.userInput.Images.NextID
	m.userInput.RestoreQueuedImages(item.Content, item.Images)
	cmd := m.dispatchSubmission(item.Content)
	if current := m.userInput.Textarea.Value(); current == "" || current == draft {
		if current == "" {
			m.userInput.Textarea.SetValue(draft)
			m.userInput.Textarea.CursorEnd()
		}
		m.userInput.Images.Pending = draftImages
		m.userInput.Images.NextID = draftNextID
	}
	return cmd
}

// SubmitToAgent is the single exit point for "send this message to the
// agent" — user Enter, slash command output, skill button, cron fire,
// hook continuation, hub notification. Ensures the agent session is up,
// pushes the already-identified message onto its inbox, returns the
// outbox-poll cmd.
// On no-provider or ensureAgentRuntime failure, posts a notice and
// returns a commit cmd (the agent is not contacted).
func (m *model) SubmitToAgent(msg core.Message) tea.Cmd {
	return m.submit(pendingMessage{Message: msg, FromUser: true})
}

// submit is SubmitToAgent for any message, a harness one included.
func (m *model) submit(p pendingMessage) tea.Cmd {
	if p.Recording != nil {
		if m.env.LLMProvider == nil {
			closeAutopilot(p.Recording)
			return m.notifyNoProvider()
		}
		if !m.pendingMessages.CanDeliver(false) || m.pendingMessages.Len() != 0 || m.conv.Stream.Active || m.conv.Compact.Active {
			closeAutopilot(p.Recording)
			return nil
		}
		startCmd, err := m.ensureAgentRuntime()
		if err != nil {
			closeAutopilot(p.Recording)
			m.conv.AddNotice("Failed to start agent: " + err.Error())
			return tea.Batch(m.CommitMessages()...)
		}
		m.pendingStopHook = nil
		m.pendingMessages.Enqueue(p)
		m.env.DetectThinkingKeywords(p.Text())
		return tea.Batch(startCmd, m.sendPendingMessages())
	}
	m.pendingStopHook = nil
	m.pendingMessages.Enqueue(p)
	if !m.pendingMessages.CanDeliver(false) {
		return tea.Batch(m.CommitMessages()...)
	}
	content := p.Text()
	log.Logger().Sugar().Debugf("SubmitToAgent: %q", kit.TruncateText(content, 60))
	if m.env.LLMProvider == nil {
		return m.notifyNoProvider()
	}

	startCmd, err := m.ensureAgentRuntime()
	if err != nil {
		m.conv.AddNotice("Failed to start agent: " + err.Error())
		return tea.Batch(m.CommitMessages()...)
	}

	m.env.DetectThinkingKeywords(content)

	sendCmd := m.sendPendingMessages()
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
	msg, _ := m.conv.Append(core.ChatMessage{Role: core.ChatUser, Content: fullMsg, DisplayContent: displayMsg}).ToMessage()
	if pluginRoot != "" {
		if m.pendingMessages.Cancelling() != nil {
			m.pendingPluginRoot = pluginRoot
		} else {
			m.services.Agent.SetPluginRoot(pluginRoot)
		}
	}
	return m.SubmitToAgent(msg)
}
