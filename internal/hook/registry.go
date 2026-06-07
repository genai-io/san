package hook

import (
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/setting"
)

type matchedHook struct {
	Event   EventType
	Matcher string
	Source  string
	Command *setting.HookCmd
	Func    *FunctionHook
}

func (e *Engine) getMatchingHooks(event EventType, input *HookInput) []matchedHook {
	e.populateInputFields(input, event)
	matchValue := getMatchValue(event, *input)

	e.mu.RLock()
	settings := e.settings
	e.mu.RUnlock()

	var matched []matchedHook
	for _, source := range e.store.CollectHooks(event, settings) {
		if !matchesEvent(source.Matcher, matchValue) {
			continue
		}
		for _, cmd := range source.Hooks {
			if !e.matchesIfCondition(cmd, *input) {
				continue
			}
			cmdCopy := cmd
			hook := matchedHook{
				Event:   event,
				Matcher: source.Matcher,
				Command: &cmdCopy,
				Source:  source.Source,
			}
			if !e.shouldRunHook(hook) {
				continue
			}
			matched = append(matched, hook)
		}
	}
	for _, source := range e.store.CollectFunctionHooks(event) {
		if !matchesEvent(source.Matcher, matchValue) {
			continue
		}
		for _, fn := range source.Hooks {
			fnCopy := fn
			hook := matchedHook{
				Event:   event,
				Matcher: source.Matcher,
				Func:    &fnCopy,
				Source:  source.Source,
			}
			if !e.shouldRunHook(hook) {
				continue
			}
			matched = append(matched, hook)
		}
	}
	return matched
}

func (e *Engine) populateInputFields(input *HookInput, event EventType) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	input.SessionID = e.sessionID
	input.TranscriptPath = e.transcriptPath
	input.Cwd = e.cwd
	input.HookEventName = string(event)

	switch event {
	case SessionStart, SessionEnd, Notification, SubagentStart, PreCompact:
	default:
		input.PermissionMode = e.permissionMode
	}
}

func (e *Engine) matchesIfCondition(cmd setting.HookCmd, input HookInput) bool {
	if cmd.If == "" {
		return true
	}
	switch input.HookEventName {
	case string(PreToolUse), string(PostToolUse), string(PostToolUseFailure), string(PermissionRequest), string(PermissionDenied):
		rule := setting.BuildRule(input.ToolName, input.ToolInput)
		if setting.MatchesToolPattern(input.ToolName, input.ToolInput, rule, cmd.If) {
			return true
		}
		if input.ToolName == "Bash" {
			if raw, ok := input.ToolInput["command"].(string); ok {
				return setting.MatchRule("Bash("+strings.TrimSpace(raw)+")", cmd.If)
			}
		}
		return false
	default:
		return false
	}
}

func (e *Engine) shouldRunHook(hook matchedHook) bool {
	if !hookRunsOnce(hook) {
		return true
	}

	key := fmt.Sprintf("%s|%s|%s|%s|%s", hook.Event, hook.Source, hook.Matcher, matchedHookType(hook), matchedHookIdentity(hook))
	return e.store.CheckOnce(key)
}

func hookRunsOnce(hook matchedHook) bool {
	if hook.Command != nil {
		return hook.Command.Once
	}
	if hook.Func != nil {
		return hook.Func.Once
	}
	return false
}

func matchedHookType(hook matchedHook) string {
	if hook.Func != nil {
		return "function"
	}
	if hook.Command != nil {
		return normalizedHookType(*hook.Command)
	}
	return ""
}

func matchedHookIdentity(hook matchedHook) string {
	if hook.Func != nil {
		return hook.Func.ID
	}
	if hook.Command != nil {
		return hookIdentity(*hook.Command)
	}
	return ""
}

func matchedHookStatusMessage(hook matchedHook) string {
	if hook.Func != nil {
		return hook.Func.StatusMessage
	}
	if hook.Command != nil {
		return hook.Command.StatusMessage
	}
	return ""
}

func normalizedHookType(cmd setting.HookCmd) string {
	if cmd.Type == "" {
		return "command"
	}
	return cmd.Type
}

func hookIdentity(cmd setting.HookCmd) string {
	switch normalizedHookType(cmd) {
	case "prompt", "agent":
		return cmd.Prompt + "|" + cmd.If + "|" + cmd.Model
	case "http":
		return cmd.URL + "|" + cmd.If
	default:
		return cmd.Command + "|" + cmd.If + "|" + cmd.Shell
	}
}
