// Compact state, message types, commands, and helper functions for conversation
// compaction and token-limit management.
package conv

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/core/system"
	"github.com/genai-io/gen-code/internal/hook"
	"github.com/genai-io/gen-code/internal/llm"
)

// --- Message types ---

// CompactResultMsg is sent when a compaction operation completes.
type CompactResultMsg struct {
	Summary       string
	OriginalCount int
	Trigger       string // "manual" or "auto"
	Error         error
}

// --- Compact state ---

const PhaseSummarizing = "Summarizing conversation history"

type CompactState struct {
	Active            bool
	Focus             string
	LastResult        string
	LastError         bool
	Phase             string
	WarningSuppressed bool
}

func (c *CompactState) Reset() {
	c.Active = false
	c.Focus = ""
	c.LastResult = ""
	c.LastError = false
	c.Phase = ""
	c.WarningSuppressed = false
}

func (c *CompactState) ClearResult() {
	c.LastResult = ""
	c.LastError = false
}

func (c *CompactState) Complete(result string, isError bool) {
	c.Active = false
	c.Focus = ""
	c.LastResult = result
	c.LastError = isError
	c.Phase = ""
	if !isError {
		c.WarningSuppressed = true
	}
}

func CompactConversation(ctx context.Context, c *llm.Client, msgs []core.Message, focus string) (summary string, count int, err error) {
	count = len(msgs)

	conversationText := core.BuildCompactionText(msgs)

	if focus != "" {
		conversationText += fmt.Sprintf("\n\n**Important**: Focus the summary on: %s", focus)
	}

	response, err := c.Complete(ctx,
		system.CompactPrompt(),
		[]core.Message{core.UserMessage(conversationText, nil)},
		core.CompactMaxTokens,
	)
	if err != nil {
		return "", count, fmt.Errorf("failed to generate summary: %w", err)
	}

	summary = strings.TrimSpace(response.Content)
	if summary == "" {
		return "", count, fmt.Errorf("compaction produced empty summary")
	}

	return summary, count, nil
}

func RenderCompactStatus(width int, spinnerView string, state CompactState) string {
	if !state.Active && state.LastResult == "" {
		return ""
	}

	label := "SESSION SUMMARY"
	title := "Conversation compacted"
	subtitle := "Older context was folded into a shorter summary. You can continue normally."
	detail := state.LastResult
	accent := kit.CurrentTheme.Success
	icon := "✓"

	if state.Active {
		if state.Phase != "" {
			title = spinnerView + " " + state.Phase
		} else {
			title = spinnerView + " Compacting conversation"
		}
		subtitle = "Summarizing recent history into a shorter reusable summary."
		if strings.TrimSpace(state.Focus) != "" {
			detail = "Focus: " + state.Focus
		} else {
			detail = "Preparing a smaller conversation state for the next turns."
		}
		accent = kit.CurrentTheme.Primary
		icon = ""
	} else if state.LastError {
		label = "COMPACT ERROR"
		title = "Compact failed"
		subtitle = "Conversation history was not replaced. You can retry once the issue is resolved."
		accent = kit.CurrentTheme.Error
		icon = "✗"
	}

	if icon != "" {
		title = icon + " " + title
	}

	boxWidth := kit.CalculateBoxWidth(width)

	labelStyle := lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.TextDim).
		Bold(true)
	headerStyle := lipgloss.NewStyle().
		Foreground(accent).
		Bold(true)
	subtitleStyle := lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.Text)
	bodyStyle := lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.TextDim)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Background(kit.CurrentTheme.Background).
		Padding(0, 1).
		Width(boxWidth).
		MarginLeft(1)

	var lines []string
	lines = append(lines, labelStyle.Render(label))
	lines = append(lines, headerStyle.Render(title))
	if strings.TrimSpace(subtitle) != "" {
		lines = append(lines, subtitleStyle.Render(subtitle))
	}
	if strings.TrimSpace(detail) != "" {
		lines = append(lines, bodyStyle.Render(detail))
	}

	return boxStyle.Render(strings.Join(lines, "\n"))
}

// --- Compact command ---

// CompactRequest holds all parameters needed to perform a conversation compaction.
type CompactRequest struct {
	Ctx        context.Context
	Client     *llm.Client
	Messages   []core.Message
	Focus      string
	HookEngine hook.Handler
	Trigger    string
}

func CompactCmd(req CompactRequest) tea.Cmd {
	return func() tea.Msg {
		ctx := req.Ctx
		focus := req.Focus
		if req.HookEngine != nil {
			outcome := req.HookEngine.Execute(ctx, hook.PreCompact, hook.HookInput{
				Trigger:            req.Trigger,
				CustomInstructions: req.Focus,
			})
			if outcome.AdditionalContext != "" {
				if focus != "" {
					focus += "\n" + outcome.AdditionalContext
				} else {
					focus = outcome.AdditionalContext
				}
			}
		}
		summary, count, err := CompactConversation(ctx, req.Client, req.Messages, focus)
		return CompactResultMsg{Summary: summary, OriginalCount: count, Trigger: req.Trigger, Error: err}
	}
}
