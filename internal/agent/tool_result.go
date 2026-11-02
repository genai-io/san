package agent

import (
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/tool"
)

// ToolResultEffects connects result policy to the opened workspace. Callbacks
// run synchronously, before the PostTool hook is scheduled.
type ToolResultEffects struct {
	ChangeCwd      func(string)
	ReloadPersonas func(string)
	TouchFile      func(string)
}

// ProcessToolResult applies foreground workspace effects before scheduling PostTool hooks.
// The caller rejects stale/cancelled results and consumes each handoff once; workers stay separate.
func ProcessToolResult(tr core.ToolResult, sideEffect any, hooks *hook.Engine, effects ToolResultEffects) {
	if resp, ok := sideEffect.(map[string]any); ok {
		switch tr.ToolName {
		case tool.ToolBash, tool.ToolPowerShell:
			if cwd := resultString(resp, "cwd"); cwd != "" && effects.ChangeCwd != nil {
				effects.ChangeCwd(cwd)
			}
		case "Write", "Edit":
			if path := resultString(resp, "filePath"); path != "" {
				hooks.ExecuteAsync(hook.FileChanged, hook.HookInput{FilePath: path, Source: tr.ToolName, Event: "change"})
				if effects.ReloadPersonas != nil {
					effects.ReloadPersonas(path)
				}
				if effects.TouchFile != nil {
					effects.TouchFile(path)
				}
			}
		case "Read":
			if file, ok := resp["file"].(map[string]any); ok {
				if path := resultString(file, "filePath"); path != "" && effects.TouchFile != nil {
					effects.TouchFile(path)
				}
			}
		}
	}

	event := hook.PostToolUse
	if tr.IsError {
		event = hook.PostToolUseFailure
	}
	response := any(tr.Content)
	if sideEffect != nil {
		response = sideEffect
	}
	input := hook.HookInput{ToolName: tr.ToolName, ToolUseID: tr.ToolCallID, ToolResponse: response}
	if tr.IsError {
		input.Error = tr.Content.Text()
	}
	hooks.ExecuteAsync(event, input)
}

// Retain the native handoff's permissive string conversion.
func resultString(values map[string]any, key string) string {
	switch value := values[key].(type) {
	case nil:
		return ""
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return fmt.Sprint(value)
	}
}
