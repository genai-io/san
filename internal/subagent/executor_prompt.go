package subagent

import (
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
)

func effectiveToolConstraints(config *AgentConfig, permMode PermissionMode, schemas []core.ToolSchema) []string {
	var constraints []string
	for _, schema := range schemas {
		if NormalizePermissionMode(string(permMode)) == PermissionExplore && tool.IsShellTool(schema.Name) {
			constraints = append(constraints, schema.Name+" limited to commands classified as read-only")
			continue
		}
		if NormalizePermissionMode(string(permMode)) == PermissionAcceptEdits && tool.IsShellTool(schema.Name) {
			constraints = append(constraints, schema.Name+" limited to read-only commands unless explicitly allowed; tests/build require an explicit allowed command or parent validation. Tool availability is not execution approval. Do not retry denied commands; report the exact command and ask the parent to validate.")
		}
		for _, rule := range config.AllowTools {
			if rule.Pattern != "" && rule.names(schema.Name) {
				constraints = append(constraints, rule.Rule())
			}
		}
	}
	return constraints
}

// buildBrief renders the SubagentBrief consumed by system.WithSubagentIdentity.
// It bundles the charter and preloaded skills. buildAgent adds the constraints
// and reading guidance after resolving the tools actually offered to this run.
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
			body := skill.Default().SkillInvocationPrompt(name)
			if body != "" {
				sb.WriteString(body)
				sb.WriteString("\n\n")
			}
		}
		custom = strings.TrimRight(sb.String(), "\n")
	}

	return system.SubagentBrief{
		AgentName:    config.Name,
		Description:  config.Description,
		Mode:         string(permMode),
		CustomPrompt: custom,
	}
}

// toolActivityParams maps tool names to the parameter key used for display.
var toolActivityParams = map[string]string{
	"Read":       "file_path",
	"Write":      "file_path",
	"Edit":       "file_path",
	"Bash":       "command",
	"PowerShell": "command",
	"WebFetch":   "url",
	"WebSearch":  "query",
	"TaskCreate": "subject",
	"TaskUpdate": "taskId",
	"TaskGet":    "taskId",
}

// formatToolActivity creates an activity line for a tool call in ToolName(args) format.
func formatToolActivity(toolName string, params map[string]any) string {
	return formatToolActivityLimit(toolName, params, 60)
}

func formatToolActivityLimit(toolName string, params map[string]any, maxChars int) string {
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

	if maxChars <= 0 {
		maxChars = 60
	}
	if text := []rune(value); len(text) > maxChars {
		value = string(text[:maxChars-1]) + "…"
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
	requestedName, _ := params["name"].(string)
	agentName := requestedName
	mode, _ := params["mode"].(string)
	desc, _ := params["description"].(string)
	if desc == "" {
		desc, _ = params["prompt"].(string)
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
	}

	agentName = displayAgentName(agentName, PermissionMode(mode))
	if desc == "" {
		return fmt.Sprintf("Agent - %s", agentName)
	}
	return fmt.Sprintf("Agent - %s: %s", agentName, desc)
}

func (e *Executor) displayNameFor(config *AgentConfig, req tool.AgentExecRequest) string {
	return displayAgentName(config.Name, e.requestPermissionMode(config, req))
}

func (e *Executor) requestPermissionMode(config *AgentConfig, req tool.AgentExecRequest) PermissionMode {
	// Explicit explore is a read-only ceiling; edit preserves the accept-edits
	// policy. An unnamed definition snapshots the parent session, while a named
	// definition retains its configured policy.
	switch strings.TrimSpace(req.Mode) {
	case "explore":
		return PermissionExplore
	case "edit":
		return PermissionAcceptEdits
	case "", "default":
		if strings.TrimSpace(req.Agent) == "" || config.displayOnly {
			return e.currentParentPermissionMode()
		}
		return NormalizePermissionMode(string(config.PermissionMode))
	default:
		return PermissionMode(strings.TrimSpace(req.Mode))
	}
}

func displayAgentName(name string, mode PermissionMode) string {
	if strings.TrimSpace(name) == "" {
		switch NormalizePermissionMode(string(mode)) {
		case PermissionExplore, PermissionDontAsk:
			return "Explorer"
		case PermissionAcceptEdits, PermissionAuto:
			return "Editor"
		case PermissionDefault, PermissionBypass:
			return "General"
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
		return "YOLO"
	case PermissionDontAsk:
		return "Don't Ask"
	case PermissionAuto:
		return "Auto"
	default:
		return "Default"
	}
}
