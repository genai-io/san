package subagent

import (
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
)

func (e *Executor) buildBrief(permMode PermissionMode) system.SubagentBrief {
	constraints := []string(nil)
	if permMode == PermissionExplore {
		constraints = []string{"Bash limited to commands classified as read-only"}
	}
	return system.SubagentBrief{
		AgentName:       defaultAgentName,
		Description:     defaultAgentDescription,
		Mode:            string(permMode),
		ToolConstraints: constraints,
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
	mode, _ := params["mode"].(string)
	desc, _ := params["description"].(string)
	if desc == "" {
		desc, _ = params["prompt"].(string)
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
	}

	agentName := displayAgentName("subagent", PermissionMode(mode))
	if desc == "" {
		return agentName
	}
	return fmt.Sprintf("%s: %s", agentName, desc)
}

func (e *Executor) displayNameFor(req tool.AgentExecRequest) string {
	if req.Name != "" {
		return req.Name
	}
	return displayAgentName(defaultAgentName, e.requestPermissionMode(req))
}

// requestPermissionMode resolves the model-facing mode without allowing it to
// name privileged runtime policies. Explicit explore is a read-only ceiling;
// edit preserves the existing accept-edits policy; default inherits the actual
// parent session policy supplied by the app.
func (e *Executor) requestPermissionMode(req tool.AgentExecRequest) PermissionMode {
	switch strings.TrimSpace(req.Mode) {
	case "explore":
		return PermissionExplore
	case "edit":
		return PermissionAcceptEdits
	case "", "default":
		return e.currentParentPermissionMode()
	default:
		return PermissionMode(strings.TrimSpace(req.Mode))
	}
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
