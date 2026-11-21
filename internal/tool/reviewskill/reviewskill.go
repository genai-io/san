// Package reviewskill provides the main agent's trigger for Auto Skill review.
// The tool acknowledges the request; the app decides at turn end whether to
// run the background reviewer.
package reviewskill

import (
	"context"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const IconReviewSkill = "✦"

// Capabilities describes what an Auto Skill review is allowed to persist.
type Capabilities struct {
	CreateSkills bool // the review may create new skills
	UpdateSkills bool // the review may refine existing skills
	DeleteSkills bool // the review may retire existing skills
}

// Active reports whether any review capability is enabled.
func (c Capabilities) Active() bool {
	return c.CreateSkills || c.UpdateSkills || c.DeleteSkills
}

const description = "Request an Auto Skill review after a successful turn."

// Schema builds the model-facing definition with guidance for the enabled
// capabilities. The main agent injects it only when caps.Active().
func Schema(caps Capabilities) core.ToolSchema {
	var reasons []string
	if caps.CreateSkills {
		reasons = append(reasons, "- a reusable technique, fix, or pattern worth capturing as a new skill")
	}
	if caps.UpdateSkills || caps.DeleteSkills {
		reasons = append(reasons, "- an existing skill you used this turn proved wrong, incomplete, or no longer useful")
	}

	desc := description + "\n\n" +
		"Call this near the end of the turn, only if you found something worth acting on for future sessions:\n" +
		strings.Join(reasons, "\n") + "\n\n" +
		"The background reviewer decides whether to change any skills; this tool does not edit them. " +
		"Auto Skill review does not restrict user-requested skill authoring. Facts about the user or project belong in memory, not skills. " +
		"Do not call for routine work, one-off task state, transient errors, or environment-specific noise. Most turns do not need a review."

	return core.ToolSchema{
		Name:        tool.ToolReviewSkill,
		Description: desc,
		Definition: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{
					"type":        "string",
					"description": "One short clause naming the reusable technique or the existing skill issue to review.",
				},
			},
		},
	}
}

// ReviewSkillTool is the model-facing trigger for Auto Skill review.
type ReviewSkillTool struct{}

func (t *ReviewSkillTool) Name() string        { return tool.ToolReviewSkill }
func (t *ReviewSkillTool) Description() string { return description }
func (t *ReviewSkillTool) Icon() string        { return IconReviewSkill }

// Schema returns the full-capability definition for the registry. The main
// agent uses the capability-tailored package-level Schema instead.
func (t *ReviewSkillTool) Schema() core.ToolSchema {
	return Schema(Capabilities{
		CreateSkills: true,
		UpdateSkills: true,
		DeleteSkills: true,
	})
}

// Execute acknowledges the request without promising a review will run.
func (t *ReviewSkillTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	reason := tool.GetString(params, "reason")
	out := "Requested Auto Skill review after this turn completes successfully."
	if reason != "" {
		out = "Requested Auto Skill review after this turn completes successfully: " + reason
	}
	return toolresult.ToolResult{
		Success: true,
		Output:  out,
		Metadata: toolresult.ResultMetadata{
			Title:    tool.ToolReviewSkill,
			Icon:     IconReviewSkill,
			Subtitle: "Auto Skill review requested",
		},
	}
}

func init() {
	tool.Register(&ReviewSkillTool{})
}
