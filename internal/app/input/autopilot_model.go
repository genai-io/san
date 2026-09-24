// Model sub-view for the /autopilot panel: picks the model the copilot's judge
// runs on — provider, then one of its models, then (for a model that reasons)
// its thinking rung. The pick is stored as a
// "vendor/model" ref so it stays pinned to its provider even when the session
// model changes. Leaving it unset keeps the session model.
package input

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/setting"
)

// apModelListRows caps how many model rows the picker shows at once.
const apModelListRows = 12

// sessionModelLabel names the unset choice: the judge follows the session model.
const sessionModelLabel = "session model"

// defaultEffortLabel names the unset rung: the model's own default thinking.
const defaultEffortLabel = "default"

// SetModelSource wires the getter for selectable "vendor/model" refs.
func (p *AutopilotSelector) SetModelSource(fn func() []string) { p.modelSource = fn }

// SetEffortSource wires the lookup for a ref's reasoning rungs.
func (p *AutopilotSelector) SetEffortSource(fn func(ref string) []string) { p.effortSource = fn }

func (p *AutopilotSelector) beginModelPick() {
	p.models = nil
	if p.modelSource != nil {
		p.models = p.modelSource()
	}
	p.pickVendor, p.pickModel, p.efforts = "", "", nil
	p.modelFilter = ""
	p.modelCursor = 0
	if vendor, _, ok := llm.ParseVendorModel(p.snap.Model); ok {
		p.modelCursor = max(slices.Index(p.modelChoices(), string(vendor)), 0)
	}
	p.view = apModel
}

// modelChoices lists the current stage's rows: the session model plus each
// provider; then a provider's models matching the filter; then, for a model that
// reasons, its thinking rungs.
func (p *AutopilotSelector) modelChoices() []string {
	switch {
	case p.pickModel != "":
		return append([]string{defaultEffortLabel}, p.efforts...)
	case p.pickVendor != "":
		var out []string
		q := strings.ToLower(p.modelFilter)
		for _, ref := range p.models {
			vendor, id, _ := llm.ParseVendorModel(ref)
			if string(vendor) == p.pickVendor && strings.Contains(strings.ToLower(id), q) {
				out = append(out, id)
			}
		}
		return out
	default:
		out := []string{sessionModelLabel}
		for _, ref := range p.models {
			if vendor, _, ok := llm.ParseVendorModel(ref); ok && !slices.Contains(out, string(vendor)) {
				out = append(out, string(vendor))
			}
		}
		return out
	}
}

func (p *AutopilotSelector) handleModelKey(msg tea.KeyMsg) tea.Cmd {
	choices := p.modelChoices()
	switch msg.String() {
	case "esc":
		switch {
		case p.pickModel != "":
			p.pickModel, p.efforts, p.modelCursor = "", nil, 0
		case p.pickVendor != "":
			p.pickVendor, p.modelFilter, p.modelCursor = "", "", 0
		default:
			p.view = apMenu
		}
	case "up":
		p.modelCursor = max(p.modelCursor-1, 0)
	case "down":
		p.modelCursor = min(p.modelCursor+1, max(len(choices)-1, 0))
	case "enter", "space":
		if len(choices) == 0 {
			return nil
		}
		pick := choices[min(p.modelCursor, len(choices)-1)]
		switch {
		case p.pickModel != "":
			p.setJudgeModel(p.pickModel, pick)
		case p.pickVendor != "":
			ref := p.pickVendor + "/" + pick
			if p.effortSource != nil {
				p.efforts = p.effortSource(ref)
			}
			if len(p.efforts) == 0 {
				p.setJudgeModel(ref, "")
				break
			}
			p.pickModel, p.modelCursor = ref, 0
			if ref == p.snap.Model {
				p.modelCursor = max(slices.Index(p.modelChoices(), p.snap.ThinkingEffort), 0)
			}
			return nil
		case pick == sessionModelLabel:
			p.setJudgeModel("", "")
		default:
			p.pickVendor, p.modelFilter, p.modelCursor = pick, "", 0
			return nil
		}
		p.view = apMenu
	case "backspace":
		if r := []rune(p.modelFilter); p.filtering() && len(r) > 0 {
			p.modelFilter = string(r[:len(r)-1])
			p.modelCursor = 0
		}
	default:
		if t := msg.Key().Text; p.filtering() && t != "" {
			p.modelFilter += t
			p.modelCursor = 0
		}
	}
	return nil
}

// filtering reports the model stage, where typed text narrows the list.
func (p *AutopilotSelector) filtering() bool { return p.pickVendor != "" && p.pickModel == "" }

// setJudgeModel stores the pick. The rung label "default" stores nothing so the
// model's own default applies.
func (p *AutopilotSelector) setJudgeModel(ref, effort string) {
	if effort == defaultEffortLabel {
		effort = ""
	}
	p.snap.Model, p.snap.ThinkingEffort = ref, effort
}

func (p *AutopilotSelector) renderModel() string {
	var b strings.Builder
	switch {
	case p.pickModel != "":
		b.WriteString(apDescStyle.Render("How hard it thinks before each call. Lower is faster and cheaper."))
	case p.pickVendor != "":
		b.WriteString(apLabelStyle.Render("Filter") + "  " + apValueStyle.Render(p.modelFilter) + apCursorStyle.Render("_"))
	default:
		b.WriteString(apDescStyle.Render("Pick a connected provider, or keep the session model. A cheap, fast model is usually enough."))
	}
	b.WriteString("\n\n")

	choices := p.modelChoices()
	current := p.currentModelChoice()
	start := max(min(p.modelCursor-apModelListRows/2, len(choices)-apModelListRows), 0)
	end := min(start+apModelListRows, len(choices))
	for i := start; i < end; i++ {
		text := choices[i]
		if text == current {
			text += "  ✓"
		}
		mark, label := "  ", apLabelStyle.Render(text)
		if i == p.modelCursor {
			mark, label = apCursorStyle.Render("▸ "), apBreadcrumbSubStyle.Render(text)
		}
		b.WriteString(mark + label + "\n")
	}
	if len(p.models) == 0 {
		b.WriteString("\n" + apSummaryStyle.Render("No cached models — open /model once to load a provider's list."))
	}
	return b.String()
}

// currentModelChoice names the saved setting as it appears in the current stage,
// so the list can tick it.
func (p *AutopilotSelector) currentModelChoice() string {
	v, id, _ := llm.ParseVendorModel(p.snap.Model)
	vendor := string(v)
	switch {
	case p.pickModel != "":
		if p.pickModel != p.snap.Model {
			return ""
		}
		if p.snap.ThinkingEffort == "" {
			return defaultEffortLabel
		}
		return p.snap.ThinkingEffort
	case p.pickVendor != "":
		if vendor != p.pickVendor {
			return ""
		}
		return id
	case p.snap.Model == "":
		return sessionModelLabel
	default:
		return vendor
	}
}

func (p *AutopilotSelector) modelHint() string {
	if p.filtering() {
		return kit.HintLine(keycap("↑↓")+" navigate", "type to filter", keycap("enter")+" pick", keycap("esc")+" back")
	}
	return kit.HintLine(keycap("↑↓")+" navigate", keycap("enter")+" pick", keycap("esc")+" back")
}

func (p *AutopilotSelector) modelCrumb() string {
	switch {
	case p.pickModel != "":
		return "Model › " + p.pickModel + " › Thinking"
	case p.pickVendor != "":
		return "Model › " + p.pickVendor
	default:
		return "Model"
	}
}

// modelValue is the Model row's value: the pinned ref and its thinking rung.
func modelValue(s setting.AutoPilotSettings) string {
	if s.Model == "" {
		return "same as session"
	}
	if s.ThinkingEffort == "" {
		return s.Model
	}
	return s.Model + " · " + s.ThinkingEffort
}
