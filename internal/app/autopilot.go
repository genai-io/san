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

// autopilotHint formats a plain amber "⏵ autopilot · <detail>" notice — the
// neutral copilot mark (same brand as the mode indicator). Used for the one-off
// "start failed" message; the handback / action / mission-done notices below
// carry their own arrow + colour.
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

// autopilotWithoutMission returns the config with its mission cleared — the copy
// written to settings.json as the default for new sessions. A mission is a
// one-off task that belongs to this session (it persists with the transcript and
// restores on /resume), not a global default every new session should inherit.
func autopilotWithoutMission(cfg setting.AutoPilotSettings) setting.AutoPilotSettings {
	shared := cfg.Clone()
	shared.Mission = ""
	return shared
}

// persistAutopilotDefault hot-swaps the running judge from m.env.AutoPilot and
// writes that config — minus the per-session mission — to settings.json as the
// default for new sessions. Shared tail of the panel Save/Start and the Mission
// editor's save/clear; callers set m.env.AutoPilot first.
func (m *model) persistAutopilotDefault() {
	m.rebuildAutopilotReviewer()
	if err := setting.UpdateAutoPilotAt(autopilotWithoutMission(m.env.AutoPilot), true); err != nil {
		log.Logger().Warn("persist autopilot default failed", zap.Error(err))
	}
}

// missionRefinePrompt drives the /autopilot Mission editor's ctrl+r action: it
// rewrites the user's draft into a cleaner mission. The reply IS the mission, so
// the prompt forbids any preamble or commentary.
const missionRefinePrompt = `You are helping the user craft the mission for an autonomous coding session — the single directive the autopilot copilot will steer toward.

Rewrite the user's mission draft into a clearer, more complete, self-contained directive: keep their intent and every specific they included, tighten and structure it, and add nothing they did not ask for. If it is already clear, return it largely unchanged.

Return ONLY the mission text — no preamble, no quotes, no commentary. Write it as a direct instruction to the agent: concrete, actionable, a few sentences at most.`

// missionRefine rewrites the mission draft into an improved mission. It runs on
// the configured autopilot model (falling back to the session model). Wired into
// the /autopilot panel via SetMissionRefiner.
func (m *model) missionRefine(ctx context.Context, draft string) (string, error) {
	provider, modelID := m.resolveReviewerModel(m.env.AutoPilot.Model)
	if provider == nil {
		return "", fmt.Errorf("no model connected")
	}

	resp, err := llm.Complete(ctx, provider, llm.CompletionOptions{
		Model:        modelID,
		SystemPrompt: missionRefinePrompt,
		Messages:     []core.Message{{Role: core.RoleUser, Content: draft}},
		MaxTokens:    600,
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
	return m.autopilotDecideCmd(result, false)
}

// autopilotKickCmd opens the mission hands-free — the /autopilot panel's Start
// button dispatches it after engaging AutoPilot. With a mission set and the agent
// idle it derives the first step from the mission (the same decision as TurnEnd,
// just an empty-ish transcript) and submits it, so briefing a mission and hitting
// Start is enough to begin with no opening turn to type. Returns nil when any
// precondition is unmet, the human is mid-compose (don't clobber input), or a
// decision is already in flight (don't stack on a double-press).
func (m *model) autopilotKickCmd() tea.Cmd {
	ar := m.env.AutoPilot
	if !m.autopilotEngaged() || m.autopilotDeciding {
		return nil
	}
	if m.conv.Stream.Active || strings.TrimSpace(m.userInput.FullValue()) != "" {
		return nil
	}
	if m.autopilotContinuations >= ar.ResolvedMaxContinuations() {
		return nil
	}
	return m.autopilotDecideCmd(core.Result{}, true)
}

// autopilotDecideCmd is the shared tail of the TurnEnd continuation and the Start
// kick: it resolves the mission/model, renders the recent transcript, marks the
// mode line "thinking…", and fires the async continue/done decision. The callers
// own their distinct gates (steer toggle, budget notice, mid-compose check);
// this owns the one decision pipeline so the two can't drift apart.
func (m *model) autopilotDecideCmd(result core.Result, kick bool) tea.Cmd {
	ar := m.env.AutoPilot
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
		return autopilotDecisionMsg{result: result, kick: kick, cont: cont, done: done, instruction: instruction, err: err}
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
	// A turn is already in flight (a new stream, or a compaction mid-apply) — the
	// running turn owns the lifecycle, so drop this stale decision silently.
	if m.conv.Stream.Active || m.conv.Compact.Active {
		return nil
	}
	// The human took over while we were deciding — left AutoPilot, or started
	// typing their own next message. Don't act (and never clobber their draft);
	// hand the finished turn back to them by firing the idle hooks OnTurnEnd
	// deferred to us (a kick had no prior turn, so it has none to fire).
	if !m.autopilotEngaged() || strings.TrimSpace(m.userInput.FullValue()) != "" {
		if msg.kick {
			return nil
		}
		return m.fireIdleHooksCmd(msg.result)
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
// it clears the mission and turns off the driving steers (Suggest, Question,
// TurnEnd), so the copilot stops actively driving — no more suggest, auto-answer,
// or continue — while the passive safety steers stay exactly as the user
// configured them (a Bash steer they left off is NOT flipped on, an explicit
// permission:false is NOT overridden). Session-scoped: the saved settings.json
// config (the user's template) is left untouched.
func (m *model) retireAutopilotMission() {
	m.env.AutoPilot.Mission = ""
	s := &m.env.AutoPilot.Steers
	s.Suggest, s.Question, s.TurnEnd = false, false, false
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
	// The human may have left AutoPilot (or turned the Question steer off) while
	// the answer inference ran — precisely to answer it themselves. If so, leave
	// the modal up for them.
	if !m.autopilotEngaged() || !m.env.AutoPilot.Steers.Question {
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
