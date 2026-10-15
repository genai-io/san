// Package evolve provides the Evolve tool: a lightweight signal the main agent
// calls to request a self-learning review (skills) of the work it
// just completed.
//
// The tool performs no learning itself. Its call is observed by the app, which
// fires the L1 background reviewer at turn end (see the self-learning wiring in
// internal/app). It is injected into the main agent's toolset only when
// self-learning is active — with a description tailored to the enabled
// capabilities; otherwise the tool is absent.
package evolve

import (
	"context"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const IconEvolve = "✦"

// Capabilities describes what an accepted self-learning review could persist.
// The tool's schema is built from it so the model is only told about what the
// review can actually do — with create denied, say, it is never invited to
// flag a new technique it can't save.
type Capabilities struct {
	CreateSkills bool // the review may create new skills
	UpdateSkills bool // the review may refine existing skills
	DeleteSkills bool // the review may retire existing skills
}

// Active reports whether any capability is on (⇒ the tool should be present).
func (c Capabilities) Active() bool {
	return c.CreateSkills || c.UpdateSkills || c.DeleteSkills
}

// Schema builds the Evolve tool schema advertised to the main agent, tailored
// to the enabled capabilities so the model is only invited to flag learnings
// the review can act on. The description is deliberately conservative — the
// failure mode to avoid is over-calling on routine turns. The app injects it
// via the toolset's ExtraTools hook only when caps.Active().
func Schema(caps Capabilities) core.ToolSchema {
	var reasons []string
	if caps.CreateSkills {
		reasons = append(reasons, "- a reusable technique, fix, or pattern worth capturing as a new skill")
	}
	if caps.UpdateSkills || caps.DeleteSkills {
		reasons = append(reasons, "- an existing skill you used this turn proved wrong, incomplete, or no longer useful")
	}

	desc := "Reflect on the work you just completed this turn and improve your skills.\n\n" +
		"Call this only when the turn produced a technique worth reusing in FUTURE sessions:\n" +
		strings.Join(reasons, "\n") + "\n\n" +
		"A background reviewer then decides what — if anything — to change in your skills; " +
		"you never write skills yourself. Facts about the user or project are memory, not " +
		"skills — don't call this for them. Do NOT call it for routine work, one-off " +
		"task state, transient errors, or environment-specific noise. Most turns warrant no " +
		"call — when in doubt, don't."

	return core.ToolSchema{
		Name:        tool.ToolEvolve,
		Description: desc,
		Definition: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{
					"type":        "string",
					"description": "One short clause naming what is worth learning from this turn.",
				},
			},
		},
	}
}

// EvolveTool is the model-facing trigger for a self-learning review.
type EvolveTool struct{}

func (t *EvolveTool) Name() string { return "Evolve" }
func (t *EvolveTool) Description() string {
	return "Request a self-learning review of the current turn"
}
func (t *EvolveTool) Icon() string { return IconEvolve }

// Schema returns the model-facing tool definition for Evolve. The main agent
// injects a capability-tailored schema via SchemaOptions.ExtraTools (built by
// the package-level Schema); this method returns the full-capability form to
// satisfy the tool.Tool interface.
func (t *EvolveTool) Schema() core.ToolSchema {
	return Schema(Capabilities{
		CreateSkills: true,
		UpdateSkills: true,
		DeleteSkills: true,
	})
}

// Execute just acknowledges: the app observes this call and considers a review
// at turn end, so the tool has nothing to do but confirm and echo the model's
// reason. The wording is "flagged", not "queued" — the review may still be
// skipped (turn cancelled, a prior review in flight, permissions leave the
// pass nothing to do).
func (t *EvolveTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	reason := tool.GetString(params, "reason")
	out := "Flagged this turn for a self-learning review at turn end."
	if reason != "" {
		out = "Flagged this turn for self-learning review: " + reason
	}
	return toolresult.ToolResult{
		Success: true,
		Output:  out,
		Metadata: toolresult.ResultMetadata{
			Title:    "Evolve",
			Icon:     IconEvolve,
			Subtitle: "self-learning flagged",
		},
	}
}

func init() {
	tool.Register(&EvolveTool{})
}
