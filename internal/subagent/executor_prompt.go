package subagent

import (
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
)

func effectiveToolConstraints(config *AgentConfig, permMode PermissionMode) []string {
	constraints := config.AllowTools.ConstrainedDisplayNames()
	if NormalizePermissionMode(string(permMode)) != PermissionExplore ||
		(config.AllowTools != nil && !config.AllowTools.HasName(tool.ToolBash)) {
		return constraints
	}

	filtered := make([]string, 0, len(constraints)+1)
	for _, constraint := range constraints {
		if !strings.HasPrefix(constraint, tool.ToolBash+"(") {
			filtered = append(filtered, constraint)
		}
	}
	return append(filtered, "Bash limited to commands classified as read-only")
}

// buildBrief renders the SubagentBrief consumed by system.WithSubagentIdentity.
// It bundles the agent's charter (name, description, mode, tool pattern
// constraints) plus the AGENT.md body and any preloaded skills, all of which
// land in the subagent's identity slot — there is no separate "assignment"
// section anymore.
func (e *Executor) buildBrief(config *AgentConfig, permMode PermissionMode) system.SubagentBrief {
	custom := strings.TrimSpace(config.GetSystemPrompt())

	// Preloaded skills are static configuration on AgentConfig.Skills. We
	// inline their bodies into CustomPrompt so they sit in the identity slot
	// — they are part of "who this agent is", not a runtime invocation.
	if len(config.Skills) > 0 && skill.DefaultIfInit() != nil {
		var sb strings.Builder
		if custom != "" {
			sb.WriteString(custom)
			sb.WriteString("\n\n")
		}
		for _, name := range config.Skills {
			body := skill.Default().GetSkillInvocationPrompt(name)
			if body != "" {
				sb.WriteString(body)
				sb.WriteString("\n\n")
			}
		}
		custom = strings.TrimRight(sb.String(), "\n")
	}

	return system.SubagentBrief{
		AgentName:       config.Name,
		Description:     config.Description,
		Mode:            string(permMode),
		ToolConstraints: effectiveToolConstraints(config, permMode),
		CustomPrompt:    custom,
	}
}

// toolActivityParams maps tool names to the parameter key used for display.
var toolActivityParams = map[string]string{
	"Read":       "file_path",
	"Write":      "file_path",
	"Edit":       "file_path",
	"Bash":       "command",
	"WebFetch":   "url",
	"WebSearch":  "query",
	"TaskCreate": "subject",
	"TaskUpdate": "taskId",
	"TaskGet":    "taskId",
}

// formatToolActivity creates an activity line for a tool call in ToolName(args) format.
func formatToolActivity(toolName string, params map[string]any) string {
	if toolName == "Agent" {
		if label := formatAgentActivity(params); label != "" {
			return label
		}
		return toolName
	}

	// Task tools: show "TaskXxx(#id subject)" by looking up subject from store
	if label := formatTaskToolActivity(toolName, params); label != "" {
		return label
	}

	paramKey, ok := toolActivityParams[toolName]
	if !ok {
		return fmt.Sprintf("%s()", toolName)
	}

	value, ok := params[paramKey].(string)
	if !ok {
		return fmt.Sprintf("%s()", toolName)
	}

	if len(value) > 60 {
		value = value[:57] + "..."
	}

	return fmt.Sprintf("%s(%s)", toolName, value)
}

// formatTaskToolActivity formats task tool calls with "#id subject" display.
func formatTaskToolActivity(toolName string, params map[string]any) string {
	switch toolName {
	case "TaskCreate":
		subject, _ := params["subject"].(string)
		if subject == "" {
			return ""
		}
		if len(subject) > 50 {
			subject = subject[:47] + "..."
		}
		return fmt.Sprintf("TaskCreate(%s)", subject)

	case "TaskUpdate", "TaskGet":
		taskID, _ := params["taskId"].(string)
		if taskID == "" {
			return ""
		}
		subject := ""
		if t, ok := todo.Default().Get(taskID); ok {
			subject = t.Subject
		}
		if subject != "" {
			if len(subject) > 40 {
				subject = subject[:37] + "..."
			}
			return fmt.Sprintf("%s(#%s %s)", toolName, taskID, subject)
		}
		return fmt.Sprintf("%s(#%s)", toolName, taskID)

	default:
		return ""
	}
}

func formatAgentActivity(params map[string]any) string {
	agentType, _ := params["subagent_type"].(string)
	mode, _ := params["mode"].(string)
	desc, _ := params["description"].(string)
	if desc == "" {
		desc, _ = params["prompt"].(string)
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
	}

	if agentType == "" {
		agentType = "subagent"
	}
	agentType = displayAgentName(agentType, PermissionMode(mode))
	if desc == "" {
		return fmt.Sprintf("Agent - %s", agentType)
	}
	return fmt.Sprintf("Agent - %s: %s", agentType, desc)
}

func displayNameFor(config *AgentConfig, req tool.AgentExecRequest) string {
	if req.Name != "" {
		return req.Name
	}
	return displayAgentName(config.Name, requestPermissionMode(config, req))
}

func requestPermissionMode(config *AgentConfig, req tool.AgentExecRequest) PermissionMode {
	// The Agent tool's mode="default" means "use the agent config's mode"
	// (as the tool schema documents and the permission dialog previews), so
	// only an explicit, non-"default" override replaces the config's mode.
	if req.Mode != "" && req.Mode != "default" {
		return NormalizePermissionMode(req.Mode)
	}
	return NormalizePermissionMode(string(config.PermissionMode))
}

func displayAgentName(name string, mode PermissionMode) string {
	if isGenericAgentName(name) {
		switch NormalizePermissionMode(string(mode)) {
		case PermissionExplore, PermissionDontAsk:
			return "Explorer"
		case PermissionAcceptEdits, PermissionAuto:
			return "Editor"
		case PermissionBypass:
			return "Bypass"
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "explore", "explorer":
			return "Explorer"
		case "editor":
			return "Editor"
		default:
			return "General"
		}
	}
	return shortAgentName(name)
}

func isGenericAgentName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "agent", "subagent", "general", "general-purpose", "explore", "explorer", "editor":
		return true
	default:
		return false
	}
}

func shortAgentName(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	kept := make([]string, 0, 2)
	for _, word := range words {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" || word == "current" || word == "change" || word == "changes" {
			continue
		}
		kept = append(kept, word)
		if len(kept) == 2 {
			break
		}
	}
	if len(kept) == 0 {
		return "Agent"
	}
	for i, word := range kept {
		kept[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(kept, " ")
}

func displayPermissionMode(mode PermissionMode) string {
	switch mode {
	case PermissionExplore:
		return "Explore"
	case PermissionAcceptEdits:
		return "Accept Edits"
	case PermissionBypass:
		return "Bypass"
	case PermissionDontAsk:
		return "Don't Ask"
	case PermissionAuto:
		return "Auto"
	default:
		return "Default"
	}
}
