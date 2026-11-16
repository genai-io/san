package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/session"
)

// flowAuthoringInstructions is appended only for an explicit /flow request.
const flowAuthoringInstructions = `Author one Flow blueprint, not the goal itself. This is a one-shot drafting request; no tools are available. Do not attempt commands, file writes, tool calls, or execution. Return only raw blueprint Markdown, preferably without an outer fence.
Grammar:
- Start with YAML frontmatter containing name (safe English identifier matching [A-Za-z][A-Za-z0-9_-]*), description, and max_parallel (positive integer).
- Exactly one fenced mermaid block: flowchart LR (or TD), bare node IDs, --> edges, & for parallel branches. Conditional edges use literal labels such as -->|YES|. A retry back edge must use |FAIL xN| with N from 1 to 10, and an explicit acceptance exit such as |PASS|.
- Each node has exactly one ## id section. Optional metadata lines: agent: name, mode: explore or edit, model: name, continue_on_error: true, for_each: ancestor or ancestor.field, max_workers: positive integer. Then a blank line and instructions. Omit agent/model unless the user or available catalog names a real configuration; never invent agent names.
- {{input}} is the simple input key; {{input.key}} is a named input. {{id}} is an ancestor's full final text, not structured fields. Each node has a fresh context: explicitly reference needed ancestor output. Do not use {{id.field}} for normal output. for_each consumes an ancestor JSON array (or array field), requires bounded max_workers, and exposes {{item}} or {{item.key}}.
Choose the minimum necessary topology: sequence only for actual dependencies, parallel only for independent work, for_each only for unknown item counts. Evaluator loops only for explicit acceptance criteria and always bounded. Never impose a mandatory work-then-review pipeline.
Default to mode: explore for inspection. Use edit only when requested, with human review before execution; never bypass permissions. Use known conversation context; if repository discovery is needed, make it a node rather than inventing facts or doing the goal now. A Skill remains a live reference; do not speculatively split its original instructions. Do not claim that anything was saved or run. The user will review the unsaved blueprint and manually choose save/run.

User goal (verbatim):
`

type flowDraftRequest struct {
	owner      *session.Session
	generation uint64
	cancel     context.CancelFunc
}

type flowDraftedMsg struct {
	request   *flowDraftRequest
	recording *session.Completion
	source    string
	err       error
}

func (m *model) syncFlowDraft() {
	if r := m.flowDraft; r != nil && (r.owner != m.services.Session || !m.userInput.Flow.IsGenerating(r.generation)) {
		r.cancel()
		m.flowDraft = nil
	}
}

func (m *model) generateFlow(msg input.FlowDraftRequestMsg) tea.Cmd {
	m.syncFlowDraft()
	if !m.userInput.Flow.IsGenerating(msg.Generation) {
		return nil
	}
	if m.flowDraft != nil {
		if m.flowDraft.generation == msg.Generation && m.flowDraft.owner == m.services.Session {
			return nil
		}
		m.flowDraft.cancel()
		m.flowDraft = nil
	}
	if m.env.LLMProvider == nil {
		m.userInput.Flow.DraftReady(msg.Generation, "", fmt.Errorf("select a provider before generating a Flow"))
		return nil
	}
	catalog := ""
	if m.services.Subagent != nil {
		catalog = m.services.Subagent.AgentCatalog()
	}
	if m.services.Skill != nil {
		for _, sk := range m.services.Skill.ListEnabled() {
			catalog += "\nAvailable Skill: " + sk.FullName()
		}
	}
	req := m.newSideRequest(session.PurposeFlow, catalog+"\n\n"+flowAuthoringInstructions+msg.Goal)
	req.schemas = nil // Drafting cannot advertise or execute tools.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	pending := &flowDraftRequest{owner: req.owner, generation: msg.Generation, cancel: cancel}
	var tick tea.Cmd
	if !m.needsSpinner() {
		tick = m.conv.Spinner.Tick
	}
	m.flowDraft = pending
	return tea.Batch(tick, func() tea.Msg {
		defer cancel()
		resp, err := req.complete(ctx)
		source := ""
		if err == nil {
			if len(resp.Content.ToolCalls()) != 0 {
				err = fmt.Errorf("Flow drafting returned a tool call; no tools were executed")
			} else if resp.StopReason != ai.StopEndTurn {
				err = fmt.Errorf("Flow drafting did not complete (stop reason: %q); no draft was created", resp.StopReason)
			} else {
				source = unwrapFlowDraft(resp.Content.Text())
				if strings.TrimSpace(source) == "" {
					err = fmt.Errorf("Flow drafting returned empty text")
				}
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		return flowDraftedMsg{request: pending, recording: req.recording, source: source, err: err}
	})
}

func (m *model) onFlowDrafted(msg flowDraftedMsg) tea.Cmd {
	r := msg.request
	if r == nil || m.flowDraft != r || r.owner != m.services.Session || !m.userInput.Flow.IsGenerating(r.generation) {
		closeSide(msg.recording)
		return nil
	}
	r.cancel()
	m.flowDraft = nil
	if msg.err != nil {
		closeSide(msg.recording)
	} else if !finishSide(msg.recording, "") {
		msg.err = fmt.Errorf("could not finish Flow draft recording")
	}
	// Validation and the unsaved Review editor belong to FlowView. Even malformed
	// source is handed over intact; this host never saves, launches, or repairs it.
	m.userInput.Flow.DraftReady(r.generation, msg.source, msg.err)
	return nil
}

// unwrapFlowDraft removes one optional outer Markdown fence, preserving inner
// Mermaid fences (including when the outer wrapper uses four or more ticks).
func unwrapFlowDraft(source string) string {
	source = strings.TrimSpace(source)
	lines := strings.Split(source, "\n")
	if len(lines) < 2 {
		return source
	}
	first := strings.TrimSpace(lines[0])
	n := len(first) - len(strings.TrimLeft(first, "`"))
	if n < 3 {
		return source
	}
	language := strings.ToLower(strings.TrimSpace(first[n:]))
	if language != "" && language != "markdown" && language != "md" {
		return source
	}
	if strings.TrimSpace(lines[len(lines)-1]) != strings.Repeat("`", n) {
		return source
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}
