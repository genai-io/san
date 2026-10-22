// /btw: a side question answered from the conversation as it stands, as a
// side request (see side.go).
package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
)

const btwTimeout = 2 * time.Minute

const btwInstructions = `<system-reminder>This is a side question from the user. Answer it directly in a single response from what this conversation already holds.
- You are a separate, one-off answer: the main work is not interrupted and continues on its own, and there is no follow-up turn.
- Answer only this question. Requests earlier in the conversation are the main agent's to carry out, not yours.
- Do not frame it as an interruption or mention what you were previously doing.
- You cannot use tools here, and nothing written as a tool call runs. If answering needs reading files, running commands or searching, say it can't be checked from a side question and suggest asking in the main conversation.
- If you don't know, say so; never promise to look it up.
</system-reminder>

`

// btwAsk is one /btw in flight, drawn live until its answer lands. It keeps
// the session it was asked in: an answer that outlives a /clear or /resume
// belongs to the conversation it was asked about, not the one now on screen.
type btwAsk struct {
	question  string
	sessionID string
}

type btwAnswerMsg struct {
	ask    btwAsk
	answer string
}

// sideQuestion starts answering question off the UI goroutine.
func (m *model) sideQuestion(question string) tea.Cmd {
	req := m.newSideRequest("btw", btwInstructions+question)
	ask := btwAsk{question: strings.Join(strings.Fields(question), " "), sessionID: m.sessionID()}
	// A running spinner already ticks; a second chain would double its speed.
	var tick tea.Cmd
	if !m.needsSpinner() {
		tick = m.conv.Spinner.Tick
	}
	m.btw = append(m.btw, ask)
	return tea.Batch(tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), btwTimeout)
		defer cancel()
		return btwAnswerMsg{ask: ask, answer: askSide(ctx, req)}
	})
}

func (m *model) onBtwAnswer(msg btwAnswerMsg) tea.Cmd {
	if i := slices.Index(m.btw, msg.ask); i >= 0 {
		m.btw = slices.Delete(m.btw, i, i+1)
	}
	if msg.ask.sessionID != m.sessionID() {
		return nil
	}
	m.conv.AddNotice(conv.SideNotice(msg.ask.question, msg.answer))
	return tea.Batch(m.CommitMessages()...)
}

// btwWaiting reports whether this session has a /btw still waiting.
func (m *model) btwWaiting() bool {
	return slices.ContainsFunc(m.btw, func(a btwAsk) bool { return a.sessionID == m.sessionID() })
}

// btwLive draws this session's questions still waiting on an answer.
func (m *model) btwLive() string {
	var rows []string
	for _, a := range m.btw {
		if a.sessionID == m.sessionID() {
			rows = append(rows, conv.RenderSideAnswer(a.question, m.conv.Spinner.View()+" answering…", m.env.Width, nil))
		}
	}
	return strings.TrimSuffix(strings.Join(rows, "\n"), "\n")
}

func askSide(ctx context.Context, req sideRequest) string {
	resp, err := req.complete(ctx)
	if err != nil {
		return "Error: " + err.Error()
	}
	if text := strings.TrimSpace(resp.Content.Text()); text != "" {
		return text
	}
	if calls := resp.Content.ToolCalls(); len(calls) > 0 {
		return fmt.Sprintf("(The model tried to call %s instead of answering. Ask in the main conversation.)", calls[0].Name)
	}
	return "(No answer.)"
}
