package hook

import (
	"regexp"
	"sync"
)

// matcherCache caches compiled regexes for matcher patterns.
// Matchers come from settings.json and are reused across many hook dispatches.
var matcherCache sync.Map // map[string]*regexp.Regexp (nil value = compile error)

// compileCached returns a cached compiled regex for the given matcher pattern,
// or nil if the pattern is invalid.
func compileCached(matcher string) *regexp.Regexp {
	if v, ok := matcherCache.Load(matcher); ok {
		re, _ := v.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile("^(" + matcher + ")$")
	if err != nil {
		matcherCache.Store(matcher, (*regexp.Regexp)(nil))
		return nil
	}
	matcherCache.Store(matcher, re)
	return re
}

// matchesEvent checks if a matcher pattern matches the given value.
// Empty or "*" matches everything. Matcher is regex-anchored at both ends.
func matchesEvent(matcher, matchValue string) bool {
	switch matcher {
	case "", "*":
		return true
	default:
		if re := compileCached(matcher); re != nil {
			return re.MatchString(matchValue)
		}
		return matcher == matchValue
	}
}

// getMatchValue extracts the value to match against based on event type.
func getMatchValue(event EventType, input HookInput) string {
	switch event {
	case PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest, PermissionDenied:
		return input.ToolName
	case SessionStart:
		return input.Source
	case SessionEnd:
		return input.Reason
	case Notification:
		return input.NotificationType
	case Setup:
		return input.Trigger
	case SubagentStart, SubagentStop:
		return input.AgentName
	case TaskCreated, TaskCompleted:
		return input.TaskSubject
	case ConfigChange:
		return input.Source
	case InstructionsLoaded:
		return input.FilePath
	case CwdChanged:
		return input.NewCwd
	case FileChanged:
		return input.FilePath
	case PreCompact, PostCompact:
		return input.Trigger
	case WorktreeCreate:
		return input.Name
	case WorktreeRemove:
		return input.WorktreePath
	default:
		return ""
	}
}

// eventSupportsMatcher returns true if the event type supports matcher filtering.
func eventSupportsMatcher(event EventType) bool {
	switch event {
	case UserPromptSubmit, Stop, StopFailure:
		return false
	default:
		return true
	}
}
