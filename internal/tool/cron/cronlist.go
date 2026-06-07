package cron

import (
	"context"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/cron"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// CronListTool lists all scheduled cron jobs.
type CronListTool struct{}

func (t *CronListTool) Name() string        { return "CronList" }
func (t *CronListTool) Description() string { return "List all scheduled cron jobs" }
func (t *CronListTool) Icon() string        { return "clock" }

func (t *CronListTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	jobs := cron.Default().List()
	if len(jobs) == 0 {
		return toolresult.ToolResult{
			Success: true,
			Output:  "No scheduled jobs.",
			Metadata: toolresult.ResultMetadata{
				Title: t.Name(),
				Icon:  t.Icon(),
			},
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d scheduled job(s):\n\n", len(jobs)))
	for _, j := range jobs {
		desc := cron.Describe(j.Cron)
		mode := "recurring"
		if !j.Recurring {
			mode = "one-shot"
		}
		if j.Durable {
			mode += ", durable"
		}
		sb.WriteString(fmt.Sprintf("  %s  %s (%s)\n", j.ID, desc, mode))
		sb.WriteString(fmt.Sprintf("    Prompt: %s\n", truncateText(j.Prompt, 80)))
		sb.WriteString(fmt.Sprintf("    Next:   %s\n", j.NextFire.Format("15:04:05")))
		if j.FiredCount > 0 {
			sb.WriteString(fmt.Sprintf("    Fired:  %d times (last: %s)\n", j.FiredCount, j.LastFired.Format("15:04:05")))
		}
		sb.WriteString("\n")
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  sb.String(),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("%d job(s)", len(jobs)),
		},
	}
}

func truncateText(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func init() {
	tool.Register(&CronListTool{})
}
