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
	rev.SetSystemPrompt(m.autopilotSystemPrompt())
	// Publish the judge and the config it resolved from as one snapshot so the
	// agent goroutine (steer gates + reviewer) never sees a judge/config skew;
	// Clone keeps the snapshot independent of later UI-goroutine edits.
	m.autopilot.Store(&autopilotRuntime{judge: rev, cfg: ar.Clone()})
}

// autopilotSystemPrompt resolves the copilot's shared "how it drives" system
// prompt — the general steering preamble the judge AND every app-side steer
// (rewrite / continue / question) preface their per-call task with, so all five
// speak with one configured voice. An inline SystemPrompt wins, then a readable
// SystemPromptFile, then the built-in default.
func (m *model) autopilotSystemPrompt() string {
	ar := m.env.AutoPilot
	if s := strings.TrimSpace(ar.SystemPrompt); s != "" {
		return s
	}
	if ar.SystemPromptFile != "" {
		b, err := os.ReadFile(ar.SystemPromptFile)
		if err != nil {
			log.Logger().Warn("autopilot systemPromptFile unreadable; using built-in system prompt",
				zap.String("file", ar.SystemPromptFile), zap.Error(err))
		} else if strings.TrimSpace(string(b)) != "" {
			return string(b)
		}
	}
	return reviewer.DefaultSystemPrompt()
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
	autopilotDoneMark = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
)

// autopilotHint formats a concise copilot notice: an amber "⏵ autopilot" mark
// (the same brand as the mode indicator) plus a dimmed detail. It states only
// what the copilot did — never the instruction itself, which reads back as the
// submitted message — so the transcript looks like a human driving the session,
// echoing the terse inline review-decision hints rather than a verbose insert.
func autopilotHint(detail string) string {
	return autopilotHintMark.Render("⏵ autopilot") + autopilotHintDim.Render(" · "+detail)
}

// autopilotHandback is the notice shown when the copilot returns control to the
// human — it stops mid-mission (needs a decision, or spent its continuation
// budget), so the arrow curves back to the user rather than driving forward.
// A non-empty detail (e.g. the decide error) rides dimmed after it.
func autopilotHandback(detail string) string {
	s := autopilotHintMark.Render("↩ autopilot") + autopilotHintDim.Render(" · over to you")
	if detail != "" {
		s += autopilotHintDim.Render(" · " + detail)
	}
	return s
}

// autopilotAction is the green notice for something the copilot handled itself
// (answered a question for you) — green = it kept the session moving, matching
// the ↖ continuation mark and the auto-approved decision hint.
func autopilotAction(detail string) string {
	return autopilotDoneMark.Render("⏵ autopilot") + autopilotHintDim.Render(" · "+detail)
}

// autopilotMissionDone is the green notice shown when the copilot judges the
// mission fully accomplished — a success terminal, distinct from the amber
// handback. AutoPilot stays on (retireAutopilotMission drops to the passive
// baseline), so the human keeps the auto-approve safety net.
func autopilotMissionDone() string {
	return autopilotDoneMark.Render("✓ autopilot") + autopilotHintDim.Render(" · mission complete")
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
	kick        bool // opened the mission (no prior turn) vs continued a finished one
	cont        bool
	done        bool
	instruction string
	err         error
}

const continueDecisionTask = `The agent just finished a turn and is about to hand control back to the human. Decide whether to keep it going toward the mission.

Reply with ONLY a JSON object:
{"continue": true|false, "done": true|false, "instruction": "the next thing to tell the agent"}

- continue=true (with a short, direct instruction — exactly what you'd type to the agent next) only if the mission is clearly not yet complete AND there is a concrete, safe next step you can direct.
- done=true (continue=false, instruction "") only if the mission is fully accomplished — nothing meaningful is left to do.
- continue=false, done=false (instruction "") if you are stopping but the mission is NOT complete: you are unsure, it needs a human decision, or the agent is blocked or asking for input.
When in doubt, stop (continue=false, done=false).

Judge what is already accomplished from the whole session transcript below, not the last turn alone — do not re-issue a step the transcript shows is already done.`

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
		m.conv.AddNotice(autopilotHandback("")) // spent the budget; the ↖ N/N above says why
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
	transcript := autopilotRecentTranscript(m.conv.Messages, 3000)
	systemPrompt := m.autopilotSystemPrompt()
	m.autopilotDeciding = true // shown on the mode indicator, not a transcript line
	return autopilotAsync(func(ctx context.Context) tea.Msg {
		cont, done, instruction, err := autopilotDecideContinue(ctx, provider, modelID, systemPrompt, mission, transcript)
		return autopilotDecisionMsg{result: result, cont: cont, done: done, instruction: instruction, err: err}
	})
}

// autopilotKickCmd opens the mission when the human hasn't: on entering AutoPilot
// with the TurnStart steer on, a mission set, and the agent idle, it derives the
// first step from the mission (the same decision as TurnEnd, just an empty-ish
// transcript) and submits it — so briefing a mission and switching autopilot on
// is enough to start, with no opening turn to type. Returns nil when any
// precondition is unmet, or when the human is mid-compose (don't clobber input).
func (m *model) autopilotKickCmd() tea.Cmd {
	ar := m.env.AutoPilot
	if !m.autopilotEngaged() || !ar.Steers.TurnStart {
		return nil
	}
	if m.conv.Stream.Active || strings.TrimSpace(m.userInput.FullValue()) != "" {
		return nil
	}
	if m.autopilotContinuations >= ar.ResolvedMaxContinuations() {
		return nil
	}
	mission := strings.TrimSpace(ar.Mission)
	if mission == "" {
		return nil
	}
	provider, modelID := m.resolveReviewerModel(ar.Model)
	if provider == nil {
		return nil
	}
	transcript := autopilotRecentTranscript(m.conv.Messages, 3000)
	systemPrompt := m.autopilotSystemPrompt()
	m.autopilotDeciding = true // shown on the mode indicator, not a transcript line
	return autopilotAsync(func(ctx context.Context) tea.Msg {
		cont, done, instruction, err := autopilotDecideContinue(ctx, provider, modelID, systemPrompt, mission, transcript)
		return autopilotDecisionMsg{kick: true, cont: cont, done: done, instruction: instruction, err: err}
	})
}

// autopilotRecentTranscript renders the recent human/agent turns as a compact
// "you:/agent:" transcript for the turn-end decision, so the copilot judges
// mission progress across the whole run — not from the last turn alone (which
// can't tell it an earlier step is already done). Walks back from the newest
// message within a character budget, then returns the kept turns oldest-first;
// tool-result and compact-summary rows are skipped.
func autopilotRecentTranscript(messages []core.ChatMessage, budget int) string {
	var lines []string
	used := 0
	for i := len(messages) - 1; i >= 0 && used < budget; i-- {
		msg := messages[i]
		var label string
		switch {
		case msg.Role == core.RoleUser && msg.ToolResult == nil && !core.IsCompactSummary(msg.Content):
			label = "you"
		case msg.Role == core.RoleAssistant:
			label = "agent"
		default:
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		line := label + ": " + kit.TruncateText(text, 600)
		lines = append(lines, line)
		used += len(line)
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i] // oldest-first, so it reads top-to-bottom
	}
	return strings.Join(lines, "\n")
}

func autopilotDecideContinue(ctx context.Context, provider llm.Provider, modelID, systemPrompt, mission, transcript string) (cont, done bool, instruction string, err error) {
	user := continueDecisionTask + "\n\nMission:\n" + mission + "\n\nSession so far (recent turns, oldest first):\n" + transcript
	content, err := autopilotComplete(ctx, provider, modelID, systemPrompt, user, 400)
	if err != nil {
		return false, false, "", err
	}
	var out struct {
		Continue    bool   `json:"continue"`
		Done        bool   `json:"done"`
		Instruction string `json:"instruction"`
	}
	if err := json.Unmarshal([]byte(reviewer.ExtractJSONObject(content)), &out); err != nil {
		return false, false, "", err
	}
	return out.Continue, out.Done, strings.TrimSpace(out.Instruction), nil
}

// handleAutopilotDecision acts on the copilot's turn-end or kick verdict: on
// continue it "types" the instruction into the composer and submits it (visible,
// budgeted); on done it retires the mission; otherwise it hands back. A kick (no
// prior turn) fires no idle hooks and stays quiet when it finds nothing to open.
func (m *model) handleAutopilotDecision(msg autopilotDecisionMsg) tea.Cmd {
	m.autopilotDeciding = false // the "thinking…" indicator clears here
	// The human may have started a turn, or cycled out of AutoPilot, while the
	// decision was in flight; if so, stand down entirely.
	if m.conv.Stream.Active || !m.autopilotEngaged() {
		return nil
	}
	if msg.err == nil && msg.cont && msg.instruction != "" {
		m.autopilotContinuations++
		m.autopilotContinuing = true
		m.userInput.Textarea.SetValue(msg.instruction) // visible: the copilot "types" it, then it reads back as the submitted message
		return m.handleSubmit()
	}
	if msg.err == nil && msg.done {
		m.conv.AddNotice(autopilotMissionDone())
		m.retireAutopilotMission()
		if msg.kick {
			return nil
		}
		return m.fireIdleHooksCmd(msg.result)
	}
	// Stopped without completing: hand back, surfacing a decide error so a
	// misconfigured model doesn't read as a silent "chose to stop". A kick that
	// found nothing to open stays quiet — it never took over, so there's nothing
	// to hand back — but a kick that *errored* says so.
	if msg.kick {
		if msg.err != nil {
			m.conv.AddNotice(autopilotHint("start failed · " + kit.TruncateText(msg.err.Error(), 120)))
		}
		return nil
	}
	detail := ""
	if msg.err != nil {
		detail = kit.TruncateText(msg.err.Error(), 120)
	}
	m.conv.AddNotice(autopilotHandback(detail))
	return m.fireIdleHooksCmd(msg.result)
}

// retireAutopilotMission winds down a finished mission without leaving AutoPilot:
// it clears the mission and resets the steers to the passive baseline (permission
// + bash judging), so the copilot stops actively driving — no more continue,
// rewrite, or auto-answer — and just keeps auto-approving while the human takes
// over. Session-scoped: the saved settings.json config (the user's template) is
// left untouched.
func (m *model) retireAutopilotMission() {
	m.env.AutoPilot.Mission = ""
	m.env.AutoPilot.Steers = setting.SteerSettings{BashPrompt: true} // Permission nil = on
	m.rebuildAutopilotReviewer()
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

// autopilotAsync runs one steer inference off the UI goroutine on a shared 60s
// budget — long enough for a slow model, short enough that a wedged one can't
// hang the steer — and wraps its result in the message the UI routes back.
func autopilotAsync(build func(ctx context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return build(ctx)
	}
}

// ── Question steer (#4): auto-answer AskUserQuestion ─────────────────────

// autopilotQuestionMsg carries the copilot's answer (or a defer) back to the UI.
type autopilotQuestionMsg struct {
	req     *tool.QuestionRequest
	answers map[int][]string
	answer  bool // false = defer to the human
}

const questionAnswerTask = `The agent has paused to ask the user a question. Answer it on the user's behalf ONLY when the mission makes the right choice clear and low-risk.

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
	systemPrompt := m.autopilotSystemPrompt()
	return autopilotAsync(func(ctx context.Context) tea.Msg {
		answers, ok := autopilotAnswerQuestion(ctx, provider, modelID, systemPrompt, mission, req)
		return autopilotQuestionMsg{req: req, answers: answers, answer: ok}
	})
}

func autopilotAnswerQuestion(ctx context.Context, provider llm.Provider, modelID, systemPrompt, mission string, req *tool.QuestionRequest) (map[int][]string, bool) {
	var b strings.Builder
	b.WriteString(questionAnswerTask + "\n\n")
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
	content, err := autopilotComplete(ctx, provider, modelID, systemPrompt, b.String(), 500)
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

const rewriteTask = `Rewrite the user's message into a clearer, more complete instruction for the agent, aligned with the session mission — keep the user's intent, add the specifics the mission implies, and drop nothing they asked for.

Return ONLY the rewritten message, with no preamble, quotes, or explanation. If the message is already clear and complete, return it unchanged.`

// autopilotRewriteCmd intercepts a human submission when AutoPilot mode and the
// TurnStart steer are on, returning (cmd, true) to rewrite it asynchronously
// before sending. It returns (nil, false) — proceed normally — for the re-submit
// of an already rewritten message, copilot continuations, slash commands, and
// empty input.
func (m *model) autopilotRewriteCmd(userMessage string) (tea.Cmd, bool) {
	if m.autopilotRewrote {
		m.autopilotRewrote = false // this IS the rewritten re-submit
		return nil, false
	}
	ar := m.env.AutoPilot
	if !m.autopilotEngaged() || !ar.Steers.TurnStart || m.autopilotContinuing {
		return nil, false
	}
	userMessage = strings.TrimSpace(userMessage)
	if userMessage == "" || strings.HasPrefix(userMessage, "/") {
		return nil, false // never touch slash commands or empty input
	}
	provider, modelID := m.resolveReviewerModel(ar.Model)
	if provider == nil {
		return nil, false
	}
	mission := strings.TrimSpace(ar.Mission)
	systemPrompt := m.autopilotSystemPrompt()
	m.autopilotDeciding = true // rewrite in flight — "thinking…" on the mode line
	return autopilotAsync(func(ctx context.Context) tea.Msg {
		return autopilotRewriteMsg{original: userMessage, rewritten: autopilotRewriteInput(ctx, provider, modelID, systemPrompt, mission, userMessage)}
	}), true
}

func autopilotRewriteInput(ctx context.Context, provider llm.Provider, modelID, systemPrompt, mission, userMessage string) string {
	user := userMessage
	if mission != "" {
		user = "Mission:\n" + mission + "\n\nUser's message:\n" + userMessage
	}
	out, err := autopilotComplete(ctx, provider, modelID, systemPrompt, rewriteTask+"\n\n"+user, 1000)
	if err != nil || out == "" {
		return userMessage // fail open: send the original
	}
	return out
}

// handleAutopilotRewrite re-submits the (possibly) rewritten message. When the
// rewrite changed the text, the re-submitted message wears the "↖ autopilot ·
// refined" annotation (via autopilotRefined → dispatchSubmission) instead of a
// separate notice. The message must never be lost, so this submits even if the
// user left AutoPilot while the rewrite ran.
func (m *model) handleAutopilotRewrite(msg autopilotRewriteMsg) tea.Cmd {
	m.autopilotDeciding = false
	text := msg.rewritten
	if text == "" {
		text = msg.original
	}
	m.autopilotRefined = text != msg.original
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
		m.conv.AddNotice(autopilotHandback("this question is yours"))
		return nil
	}
	m.conv.Modal.Question.Hide()
	m.conv.AddNotice(autopilotAction("answered for you"))
	return m.handleQuestionResponse(conv.QuestionResponseMsg{
		Request:  msg.req,
		Response: &tool.QuestionResponse{RequestID: msg.req.ID, Answers: msg.answers},
	})
}
