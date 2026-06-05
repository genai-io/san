package cron

import (
	"context"
	"fmt"

	"github.com/genai-io/san/v2/internal/cron"
	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/toolresult"
)

// CronDeleteTool cancels a scheduled cron job.
type CronDeleteTool struct{}

func (t *CronDeleteTool) Name() string        { return "CronDelete" }
func (t *CronDeleteTool) Description() string { return "Cancel a scheduled cron job" }
func (t *CronDeleteTool) Icon() string        { return "clock" }

func (t *CronDeleteTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	id := tool.GetString(params, "id")
	if id == "" {
		return toolresult.NewErrorResult(t.Name(), "job id is required")
	}

	if err := cron.Default().Delete(id); err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Cancelled cron job %s.", id),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("Cancelled %s", id),
		},
	}
}

func init() {
	tool.Register(&CronDeleteTool{})
}
