package input

import (
	"fmt"
	"math"
	"strings"

	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/skill"
)

// ContextDeps holds the state needed by the /context command.
type ContextDeps struct {
	CurrentModel     *llm.CurrentModelInfo
	Store            *llm.Store
	InputTokens      int
	OutputTokens     int
	TurnInputTokens  int
	TurnOutputTokens int
	ConversationCost llm.Money
	MessageCount     int
	ToolCount        int
	Skills           []*skill.Skill
	SystemPrompt     string
}

func estimateTokens(s string) int {
	return int(math.Ceil(float64(len([]rune(s))) / 4.0))
}

// FormatContextInfo returns a formatted string with current context usage info.
func FormatContextInfo(deps ContextDeps) string {
	modelID := "<unknown>"
	provider := ""
	inputLimit := 0

	if deps.CurrentModel != nil {
		modelID = deps.CurrentModel.ModelID
		provider = string(deps.CurrentModel.Provider)
		inputLimit, _ = kit.GetModelTokenLimits(deps.Store, deps.CurrentModel)
	}

	totalTokens := deps.TurnInputTokens
	if totalTokens == 0 {
		totalTokens = deps.InputTokens
	}

	var usagePct float64
	if inputLimit > 0 {
		usagePct = float64(totalTokens) / float64(inputLimit) * 100
	}

	// Estimate per-category breakdown
	promptTokens := estimateTokens(deps.SystemPrompt)
	if promptTokens > totalTokens {
		promptTokens = totalTokens
	}
	messagesTokens := totalTokens - promptTokens

	// Roughly split prompt into tools vs base sections
	toolPromptTokens := 0
	skillPromptTokens := 0
	if deps.ToolCount > 0 && promptTokens > 0 {
		toolPct := 0.3 // estimated portion of system prompt for tool definitions
		if deps.ToolCount > 10 {
			toolPct = 0.4
		}
		toolPromptTokens = int(float64(promptTokens) * toolPct)
	}
	if len(deps.Skills) > 0 && promptTokens > 0 {
		skillPct := 0.15 // estimated portion of system prompt for skill instructions
		if len(deps.Skills) > 8 {
			skillPct = 0.2
		}
		skillPromptTokens = int(float64(promptTokens) * skillPct)
	}
	basePromptTokens := promptTokens - toolPromptTokens - skillPromptTokens
	if basePromptTokens < 0 {
		basePromptTokens = 0
	}

	compactBuffer := 0
	freeSpace := 0
	if inputLimit > 0 {
		compactBuffer = int(float64(inputLimit) * 0.05) // 5% buffer before auto-compact at 95%
		freeSpace = inputLimit - totalTokens - compactBuffer
		if freeSpace < 0 {
			freeSpace = 0
		}
	}

	var bldr strings.Builder

	// Header
	bldr.WriteString("Context Usage\n")

	// Model
	if provider != "" {
		bldr.WriteString(fmt.Sprintf("  %s (%s)\n", modelID, provider))
	} else {
		bldr.WriteString(fmt.Sprintf("  %s\n", modelID))
	}

	// Token bar
	if inputLimit > 0 && totalTokens > 0 {
		barLen := 20
		filled := int(usagePct * float64(barLen) / 100.0)
		if filled > barLen {
			filled = barLen
		}
		bar := strings.Repeat("⛁", filled) + strings.Repeat(" ", barLen-filled)
		bldr.WriteString(fmt.Sprintf("  %s  %s/%s tokens (%.0f%%)\n", bar, kit.FormatTokenCount(totalTokens), kit.FormatTokenCount(inputLimit), usagePct))
	}

	// Category breakdown
	if promptTokens > 0 || messagesTokens > 0 {
		bldr.WriteString("\n  Estimated usage by category\n")

		if basePromptTokens > 0 {
			pct := float64(basePromptTokens) / float64(inputLimit) * 100
			bldr.WriteString(fmt.Sprintf("    ⛁ System prompt:   %s tokens (%.1f%%)\n", kit.FormatTokenCount(basePromptTokens), pct))
		}
		if toolPromptTokens > 0 {
			pct := float64(toolPromptTokens) / float64(inputLimit) * 100
			bldr.WriteString(fmt.Sprintf("    ⛁ System tools:   %s tokens (%.1f%%)\n", kit.FormatTokenCount(toolPromptTokens), pct))
		}
		if skillPromptTokens > 0 {
			pct := float64(skillPromptTokens) / float64(inputLimit) * 100
			bldr.WriteString(fmt.Sprintf("    ⛁ Skills:         %s tokens (%.1f%%)\n", kit.FormatTokenCount(skillPromptTokens), pct))
		}
		if messagesTokens > 0 {
			pct := float64(messagesTokens) / float64(inputLimit) * 100
			bldr.WriteString(fmt.Sprintf("    ⛁ Messages:       %s tokens (%.1f%%)\n", kit.FormatTokenCount(messagesTokens), pct))
		}
		if freeSpace > 0 {
			pct := float64(freeSpace) / float64(inputLimit) * 100
			bldr.WriteString(fmt.Sprintf("    ⛶ Free space:     %s (%.1f%%)\n", kit.FormatTokenCount(freeSpace), pct))
		}
		if compactBuffer > 0 {
			pct := float64(compactBuffer) / float64(inputLimit) * 100
			bldr.WriteString(fmt.Sprintf("    ⛝ Autocompact buffer: %s tokens (%.1f%%)\n", kit.FormatTokenCount(compactBuffer), pct))
		}
	}

	// Skills
	if len(deps.Skills) > 0 {
		bldr.WriteString("\n  Skills · /skills\n\n")
		// Group by scope
		type skillGroup struct {
			label string
			list  []*skill.Skill
		}
		builtin := skillGroup{label: "Built-in"}
		user := skillGroup{label: "User"}
		project := skillGroup{label: "Project"}
		other := skillGroup{label: "Other"}

		for _, sk := range deps.Skills {
			switch sk.Scope {
			case skill.ScopeClaudeUser, skill.ScopeUser:
				user.list = append(user.list, sk)
			case skill.ScopeProject, skill.ScopeClaudeProject:
				project.list = append(project.list, sk)
			case skill.ScopeUserPlugin, skill.ScopeProjectPlugin:
				other.list = append(other.list, sk)
			default:
				builtin.list = append(builtin.list, sk)
			}
		}

		groups := []skillGroup{builtin, user, project, other}
		first := true
		for _, g := range groups {
			if len(g.list) == 0 {
				continue
			}
			if first {
				bldr.WriteString(fmt.Sprintf("    %s\n", g.label))
				first = false
			} else {
				bldr.WriteString(fmt.Sprintf("\n    %s\n", g.label))
			}
			for i, sk := range g.list {
				prefix := "├"
				if i == len(g.list)-1 {
					prefix = "└"
				}
				tag := formatSkillTag(sk)
				bldr.WriteString(fmt.Sprintf("    %s %s: %s\n", prefix, sk.FullName(), tag))
			}
		}
	}

	// Cost
	if !deps.ConversationCost.IsZero() {
		bldr.WriteString(fmt.Sprintf("\n  Cost: %s\n", kit.FormatMoney(deps.ConversationCost)))
	}

	return bldr.String()
}

func formatSkillTag(sk *skill.Skill) string {
	tokens := estimateTokens(sk.Description)
	if tokens <= 0 {
		tokens = estimateTokens(sk.Name)
	}
	if tokens <= 0 {
		return "< 20 tokens"
	}
	switch {
	case tokens < 10:
		return "< 20 tokens"
	case tokens < 30:
		return fmt.Sprintf("~%d tokens", 20)
	case tokens < 60:
		return fmt.Sprintf("~%d tokens", 30)
	case tokens < 100:
		return fmt.Sprintf("~%d tokens", 60)
	case tokens < 150:
		return fmt.Sprintf("~%d tokens", 100)
	default:
		rounded := (tokens / 50) * 50
		if rounded < 50 {
			rounded = 50
		}
		return fmt.Sprintf("~%d tokens", rounded)
	}
}
