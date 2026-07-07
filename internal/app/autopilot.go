// Autopilot copilot glue: the Mission-dialog responder and the app-side message
// routing that back it. The /autopilot panel itself lives in internal/app/input;
// this file wires the pieces that need the session's LLM provider.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/reviewer"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

// autopilotRuntime is the immutable snapshot the agent goroutine reads: the live
// judge plus the resolved config it was built from. rebuildAutopilotReviewer
// swaps it as one unit so the judge and the steer gates can never skew.
type autopilotRuntime struct {
	judge *reviewer.Judge
	cfg   setting.AutoPilotSettings
}

// rebuildAutopilotReviewer builds the autopilot judge from the live session
// config and stores it in the atomic slot. Called at agent build time and again
// whenever the /autopilot panel saves, so a mid-session model / system-prompt
// change takes effect on the running agent without a restart.
func (m *model) rebuildAutopilotReviewer() {
	ar := m.env.AutoPilot
	provider, modelID := m.resolveReviewerModel(ar.Model)
	rev := reviewer.New(provider, modelID)
	switch {
	case ar.SystemPrompt != "":
		rev.SetSystemPrompt(ar.SystemPrompt)
	case ar.SystemPromptFile != "":
		if b, err := os.ReadFile(ar.SystemPromptFile); err == nil {
			rev.SetSystemPrompt(string(b))
		} else {
			log.Logger().Warn("autopilot systemPromptFile unreadable; using built-in doctrine",
				zap.String("file", ar.SystemPromptFile), zap.Error(err))
		}
	}
	// Publish the judge and the config it resolved from as one snapshot so the
	// agent goroutine (steer gates + reviewer) never sees a judge/config skew;
	// Clone keeps the snapshot independent of later UI-goroutine edits.
	m.autopilot.Store(&autopilotRuntime{judge: rev, cfg: ar.Clone()})
}

// liveAutopilotConfig returns the synchronized config snapshot for the agent
// goroutine's steer gates. Zero value until the first rebuildAutopilotReviewer
// (which runs at agent build, before the agent goroutine starts).
func (m *model) liveAutopilotConfig() setting.AutoPilotSettings {
	if rt := m.autopilot.Load(); rt != nil {
		return rt.cfg
	}
	return setting.AutoPilotSettings{}
}

// autopilotEngaged reports whether AutoPilot is the active permission posture —
// the precondition every steer shares. Steers are the copilot's actions, so
// none fire unless the copilot is actually driving. Combine with the per-steer
// toggle at each gate: `if !m.autopilotEngaged() || !<steer> { return }`.
func (m *model) autopilotEngaged() bool {
	return m.env.OperationMode == setting.ModeAutoPilot
}

var (
	autopilotHintMark = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
	autopilotHintDim  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
)

// autopilotHint formats a concise copilot notice: an amber "⏵ autopilot" mark
// (the same brand as the mode indicator) plus a dimmed detail. It states only
// what the copilot did — never the instruction itself, which reads back as the
// submitted message — so the transcript looks like a human driving the session,
// echoing the terse inline review-decision hints rather than a verbose insert.
func autopilotHint(detail string) string {
	return autopilotHintMark.Render("⏵ autopilot") + autopilotHintDim.Render(" · "+detail)
}

// settleAutopilotHint resolves the pending "thinking…" notice into its outcome
// on the same line — falling back to a fresh notice if that line already
// flushed to scrollback — so the copilot's deliberation and its decision read
// as one line instead of two.
func (m *model) settleAutopilotHint(detail string) {
	hint := autopilotHint(detail)
	if !m.conv.SetLastNotice(hint) {
		m.conv.AddNotice(hint)
	}
}

// marshalAutoPilot encodes the live config for session persistence, returning
// "" for an unset config so untouched sessions carry no autopilot state.
func marshalAutoPilot(a setting.AutoPilotSettings) string {
	if a.IsZero() {
		return ""
	}
	b, err := json.Marshal(a)
	if err != nil {
		return ""
	}
	return string(b)
}

// parseAutoPilot decodes a persisted config blob; a blank or malformed blob
// yields the zero config.
func parseAutoPilot(s string) setting.AutoPilotSettings {
	var a setting.AutoPilotSettings
	if s != "" {
		_ = json.Unmarshal([]byte(s), &a)
	}
	return a
}

// missionBriefingPrompt is the copilot's persona while the user briefs it in the
// /autopilot Mission dialog. It replies as the "co-pilot" that will steer the
// session — acknowledging the mission and stating, briefly, how it will drive.
const missionBriefingPrompt = `You are the autopilot copilot riding shotgun on a coding agent — a second set of hands that steers the session at set points (approving gray-zone tool calls, answering prompts, deciding whether to keep the agent going after a turn).

The user is briefing you on the mission for this session. Reply in 2-4 sentences: confirm the goal in your own words, and state concretely how you will steer toward it — when you will handle things yourself versus hand back to the human, and what would make you stop. Be direct and specific. Do not use lists or headers. If the briefing is ambiguous, ask one sharp clarifying question instead of guessing.`

// missionReply produces the copilot's reply to the briefing so far. It runs on
// the configured autopilot model (falling back to the session model) and returns
// a short acknowledgement of how it will steer. Wired into the /autopilot panel
// via SetMissionResponder.
func (m *model) missionReply(ctx context.Context, history []input.MissionMessage) (string, error) {
	provider, modelID := m.resolveReviewerModel(m.env.AutoPilot.Model)
	if provider == nil {
		return "", fmt.Errorf("no model connected")
	}

	msgs := make([]core.Message, 0, len(history))
	for _, h := range history {
		role := core.RoleUser
		if !h.FromUser {
			role = core.RoleAssistant
		}
		msgs = append(msgs, core.Message{Role: role, Content: h.Text})
	}

	resp, err := llm.Complete(ctx, provider, llm.CompletionOptions{
		Model:        modelID,
		SystemPrompt: missionBriefingPrompt,
		Messages:     msgs,
		MaxTokens:    400,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

// ── TurnEnd steer (#5): auto-continuation ───────────────────────────────

// autopilotDecisionMsg carries the copilot's turn-end continuation decision back
// to the UI goroutine.
type autopilotDecisionMsg struct {
	result      core.Result
	cont        bool
	instruction string
	err         error
}

const continueDecisionPrompt = `You are the autopilot copilot steering a coding agent toward a mission across turns. The agent just finished a turn and is about to hand control back to the human.

Decide whether to keep it going. Reply with ONLY a JSON object:
{"continue": true|false, "instruction": "the next thing to tell the agent"}

Set continue=true only if the mission is clearly not yet complete AND there is a concrete, safe next step you can direct. The instruction is a short, direct imperative — exactly what you'd type to the agent as the next message.
Set continue=false (with instruction "") if the mission looks complete, if you are unsure, if it needs a human decision, or if the agent is blocked or asking for input. When in doubt, stop.`

// autopilotContinueCmd asks the copilot whether to auto-continue the finished
// turn. It returns nil (letting the turn go idle normally) when AutoPilot mode
// is off, the TurnEnd steer is off, the budget is spent, there's no mission, the
// model is missing, or the turn didn't end cleanly.
func (m *model) autopilotContinueCmd(result core.Result) tea.Cmd {
	ar := m.env.AutoPilot
	if !m.autopilotEngaged() || result.StopReason != core.StopEndTurn || !ar.Steers.TurnEnd {
		return nil
	}
	if m.autopilotContinuations >= ar.ResolvedMaxContinuations() {
		m.conv.AddNotice(autopilotHint(fmt.Sprintf("budget reached (%d) · handing back", ar.ResolvedMaxContinuations())))
		return nil
	}
	mission := strings.TrimSpace(ar.Mission)
	if mission == "" {
		return nil // no mission to steer toward
	}
	provider, modelID := m.resolveReviewerModel(ar.Model)
	if provider == nil {
		return nil
	}
	last := core.LastAssistantChatContent(m.conv.Messages)
	m.conv.AddNotice(autopilotHint("thinking…"))
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cont, instruction, err := autopilotDecideContinue(ctx, provider, modelID, mission, last)
		return autopilotDecisionMsg{result: result, cont: cont, instruction: instruction, err: err}
	}
}

func autopilotDecideContinue(ctx context.Context, provider llm.Provider, modelID, mission, lastTurn string) (bool, string, error) {
	user := "Mission:\n" + mission + "\n\nThe agent's last turn ended with:\n" + kit.TruncateText(lastTurn, 2000)
	content, err := autopilotComplete(ctx, provider, modelID, continueDecisionPrompt, user, 400)
	if err != nil {
		return false, "", err
	}
	var out struct {
		Continue    bool   `json:"continue"`
		Instruction string `json:"instruction"`
	}
	if err := json.Unmarshal([]byte(reviewer.ExtractJSONObject(content)), &out); err != nil {
		return false, "", err
	}
	return out.Continue, strings.TrimSpace(out.Instruction), nil
}

// handleAutopilotDecision acts on the copilot's turn-end verdict: on continue it
// "types" the instruction into the composer and submits it (visible, budgeted);
// on stop it fires the idle hooks OnTurnEnd deferred while the decision ran.
func (m *model) handleAutopilotDecision(msg autopilotDecisionMsg) tea.Cmd {
	// The human may have started a turn while the decision was in flight; if so,
	// stand down entirely.
	if m.conv.Stream.Active {
		return nil
	}
	if msg.err == nil && msg.cont && strings.TrimSpace(msg.instruction) != "" {
		instr := strings.TrimSpace(msg.instruction)
		m.autopilotContinuations++
		m.autopilotContinuing = true
		m.settleAutopilotHint(fmt.Sprintf("continuing (%d/%d)",
			m.autopilotContinuations, m.env.AutoPilot.ResolvedMaxContinuations()))
		m.userInput.Textarea.SetValue(instr) // visible: the copilot "types" it, then it reads back as the submitted message
		return m.handleSubmit()
	}
	m.settleAutopilotHint("handing back")
	return m.fireIdleHooksCmd(msg.result)
}

// autopilotComplete runs one single-user-message completion and returns the
// trimmed reply — the shared shape of the copilot's steer inferences.
func autopilotComplete(ctx context.Context, provider llm.Provider, modelID, system, user string, maxTokens int) (string, error) {
	resp, err := llm.Complete(ctx, provider, llm.CompletionOptions{
		Model:        modelID,
		SystemPrompt: system,
		Messages:     []core.Message{{Role: core.RoleUser, Content: user}},
		MaxTokens:    maxTokens,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

// ── Question steer (#4): auto-answer AskUserQuestion ─────────────────────

// autopilotQuestionMsg carries the copilot's answer (or a defer) back to the UI.
type autopilotQuestionMsg struct {
	req     *tool.QuestionRequest
	answers map[int][]string
	answer  bool // false = defer to the human
}

const questionAnswerPrompt = `You are the autopilot copilot for a coding agent. The agent has paused to ask the user a question. Answer it on the user's behalf ONLY when the mission makes the right choice clear and low-risk.

Reply with ONLY a JSON object:
{"defer": false, "answers": {"0": ["Exact option label"], "1": ["Label A","Label B"]}}

- Keys are question indices as strings; values are arrays of the EXACT option labels you choose (copy them verbatim).
- Single-select ⇒ exactly one label; multi-select ⇒ one or more. Answer every question.
- Set "defer": true (answers {}) if you are unsure, if the choice is significant or irreversible, or if it genuinely needs the human. When in doubt, defer.`

// autopilotAnswerQuestionCmd asks the copilot to answer a pending question, or
// nil when AutoPilot mode is off, the Question steer is off, or no model is
// available.
func (m *model) autopilotAnswerQuestionCmd(req *tool.QuestionRequest) tea.Cmd {
	ar := m.env.AutoPilot
	if !m.autopilotEngaged() || !ar.Steers.Question || req == nil || len(req.Questions) == 0 {
		return nil
	}
	provider, modelID := m.resolveReviewerModel(ar.Model)
	if provider == nil {
		return nil
	}
	mission := strings.TrimSpace(ar.Mission)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		answers, ok := autopilotAnswerQuestion(ctx, provider, modelID, mission, req)
		return autopilotQuestionMsg{req: req, answers: answers, answer: ok}
	}
}

func autopilotAnswerQuestion(ctx context.Context, provider llm.Provider, modelID, mission string, req *tool.QuestionRequest) (map[int][]string, bool) {
	var b strings.Builder
	if mission != "" {
		b.WriteString("Mission:\n" + mission + "\n\n")
	}
	b.WriteString("The agent is asking:\n")
	for i, q := range req.Questions {
		fmt.Fprintf(&b, "Question %d", i)
		if q.Header != "" {
			fmt.Fprintf(&b, " [%s]", q.Header)
		}
		fmt.Fprintf(&b, ": %s\n", q.Question)
		if q.MultiSelect {
			b.WriteString("  (select one or more)\n")
		} else {
			b.WriteString("  (select exactly one)\n")
		}
		for _, opt := range q.Options {
			fmt.Fprintf(&b, "    - %s", opt.Label)
			if opt.Description != "" {
				fmt.Fprintf(&b, " — %s", opt.Description)
			}
			b.WriteString("\n")
		}
	}
	content, err := autopilotComplete(ctx, provider, modelID, questionAnswerPrompt, b.String(), 500)
	if err != nil {
		return nil, false
	}
	var out struct {
		Defer   bool                `json:"defer"`
		Answers map[string][]string `json:"answers"`
	}
	if err := json.Unmarshal([]byte(reviewer.ExtractJSONObject(content)), &out); err != nil || out.Defer {
		return nil, false
	}
	// Every question must get at least one valid (verbatim-matching) label, else
	// defer — a partial or hallucinated answer is worse than asking the human.
	answers := make(map[int][]string, len(req.Questions))
	for i, q := range req.Questions {
		chosen := validQuestionLabels(q, out.Answers[strconv.Itoa(i)])
		if len(chosen) == 0 {
			return nil, false
		}
		if !q.MultiSelect {
			chosen = chosen[:1]
		}
		answers[i] = chosen
	}
	return answers, true
}

// ── TurnStart steer (#1): rewrite the user's input ──────────────────────

// autopilotRewriteMsg carries the copilot's rewrite of a human message back to
// the UI so it can be re-submitted.
type autopilotRewriteMsg struct {
	original  string
	rewritten string
}

const rewritePrompt = `You are the autopilot copilot for a coding agent. Rewrite the user's message into a clearer, more complete instruction for the agent, aligned with the session mission — keep the user's intent, add the specifics the mission implies, and drop nothing they asked for.

Return ONLY the rewritten message, with no preamble, quotes, or explanation. If the message is already clear and complete, return it unchanged.`

// autopilotRewriteCmd intercepts a human submission when AutoPilot mode and the
// TurnStart steer are on, returning (cmd, true) to rewrite it asynchronously
// before sending. It returns (nil, false) — proceed normally — for the re-submit
// of an already rewritten message, copilot continuations, slash commands, and
// empty input.
func (m *model) autopilotRewriteCmd(raw string) (tea.Cmd, bool) {
	if m.autopilotRewrote {
		m.autopilotRewrote = false // this IS the rewritten re-submit
		return nil, false
	}
	ar := m.env.AutoPilot
	if !m.autopilotEngaged() || !ar.Steers.TurnStart || m.autopilotContinuing {
		return nil, false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "/") {
		return nil, false // never touch slash commands or empty input
	}
	provider, modelID := m.resolveReviewerModel(ar.Model)
	if provider == nil {
		return nil, false
	}
	mission := strings.TrimSpace(ar.Mission)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return autopilotRewriteMsg{original: raw, rewritten: autopilotRewriteInput(ctx, provider, modelID, mission, raw)}
	}, true
}

func autopilotRewriteInput(ctx context.Context, provider llm.Provider, modelID, mission, raw string) string {
	user := raw
	if mission != "" {
		user = "Mission:\n" + mission + "\n\nUser's message:\n" + raw
	}
	out, err := autopilotComplete(ctx, provider, modelID, rewritePrompt, user, 1000)
	if err != nil || out == "" {
		return raw // fail open: send the original
	}
	return out
}

// handleAutopilotRewrite re-submits the (possibly) rewritten message.
func (m *model) handleAutopilotRewrite(msg autopilotRewriteMsg) tea.Cmd {
	text := strings.TrimSpace(msg.rewritten)
	if text == "" {
		text = msg.original
	}
	if text != msg.original {
		m.conv.AddNotice(autopilotHint("refined your request"))
	}
	m.autopilotRewrote = true
	m.userInput.Textarea.SetValue(text)
	return m.handleSubmit()
}

// validQuestionLabels keeps only labels that match a real option verbatim,
// guarding against a hallucinated choice.
func validQuestionLabels(q tool.Question, labels []string) []string {
	valid := make([]string, 0, len(labels))
	for _, l := range labels {
		for _, opt := range q.Options {
			if l == opt.Label {
				valid = append(valid, l)
				break
			}
		}
	}
	return valid
}

// handleAutopilotQuestion applies the copilot's answer: on a real answer it hides
// the modal and replies through the same path the human uses; on a defer it
// leaves the modal up for the human.
func (m *model) handleAutopilotQuestion(msg autopilotQuestionMsg) tea.Cmd {
	// Drop if the question is no longer pending (human answered, or agent stopped
	// and drained it).
	if m.conv.Modal.PendingQuestion != msg.req || m.conv.Modal.PendingQuestionReply == nil {
		return nil
	}
	if !msg.answer || len(msg.answers) == 0 {
		m.conv.AddNotice(autopilotHint("left this question for you"))
		return nil
	}
	m.conv.Modal.Question.Hide()
	m.conv.AddNotice(autopilotHint("answered for you"))
	return m.handleQuestionResponse(conv.QuestionResponseMsg{
		Request:  msg.req,
		Response: &tool.QuestionResponse{RequestID: msg.req.ID, Answers: msg.answers},
	})
}
