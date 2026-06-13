// Side effects triggered by tool calls: cwd-changing tools (Bash, worktree),
// file-touching tools (Write/Edit/Read), agent-launching tools (background
// task tracking), and large-output persistence (oversized ToolResult.Content
// gets paged out to a blob and replaced with a preview + reference).
package app

import (
	"fmt"
	"unicode/utf8"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task/tracker"
	"github.com/genai-io/san/internal/tool"
)

func (m *model) applyToolSideEffects(toolName string, sideEffect any) {
	resp, ok := sideEffect.(map[string]any)
	if !ok {
		return
	}
	m.trackAgentLaunch(toolName, resp)
	switch toolName {
	case "Bash":
		if newCwd := kit.MapString(resp, "cwd"); newCwd != "" {
			m.changeCwd(newCwd)
		}
	case tool.ToolEnterWorktree:
		if worktreePath := kit.MapString(resp, "worktreePath"); worktreePath != "" {
			m.changeCwd(worktreePath)
		}
	case tool.ToolExitWorktree:
		if restoredPath := kit.MapString(resp, "restoredPath"); restoredPath != "" {
			m.changeCwd(restoredPath)
		}
	case "Write", "Edit":
		if filePath := kit.MapString(resp, "filePath"); filePath != "" {
			m.fireFileChanged(filePath, toolName)
			m.reloadPersonasIfChanged(filePath)
			if m.env.FileCache != nil {
				m.env.FileCache.Touch(filePath)
			}
		}
	case "Read":
		if fileData, ok := resp["file"].(map[string]any); ok {
			if filePath := kit.MapString(fileData, "filePath"); filePath != "" && m.env.FileCache != nil {
				m.env.FileCache.Touch(filePath)
			}
		}
	}
}

func (m *model) trackAgentLaunch(toolName string, resp map[string]any) {
	if !tool.IsAgentToolName(toolName) {
		return
	}
	bg, ok := resp["backgroundTask"].(map[string]any)
	if !ok {
		return
	}
	launch := tracker.BackgroundTaskLaunch{
		TaskID:      kit.MapString(bg, "taskId"),
		AgentName:   kit.MapString(bg, "agentName"),
		AgentType:   kit.MapString(bg, "agentType"),
		Description: kit.MapString(bg, "description"),
	}
	if launch.TaskID == "" {
		return
	}
	tracker.TrackWorker(m.services.Tracker, launch)
}

func (m *model) persistOverflow(result *core.ToolResult) {
	const overflowThreshold = 100_000
	const previewSize = 10_000

	if len(result.Content) <= overflowThreshold {
		return
	}
	cutoff := min(previewSize, len(result.Content))
	for cutoff > 0 && !utf8.RuneStart(result.Content[cutoff]) {
		cutoff--
	}
	preview := result.Content[:cutoff]
	persisted := false
	if err := m.services.Session.EnsureStore(m.env.CWD); err == nil && m.services.Session.ID() != "" {
		if err := m.services.Session.GetStore().PersistToolResult(m.services.Session.ID(), result.ToolCallID, result.Content); err == nil {
			persisted = true
		}
	}
	if persisted {
		result.Content = fmt.Sprintf("%s\n\n[Full output persisted to blobs/tool-result/%s/%s]", preview, m.services.Session.ID(), result.ToolCallID)
	} else {
		result.Content = fmt.Sprintf("%s\n\n[Output truncated from %d bytes — full content not persisted]", preview, len(result.Content))
	}
}
