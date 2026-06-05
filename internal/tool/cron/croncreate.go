package cron

import (
	"context"
	"fmt"

	"github.com/genai-io/san/v2/internal/cron"
	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/toolresult"
)

// CronCreateTool creates a new scheduled cron job.
type CronCreateTool struct{}

func (t *CronCreateTool) Name() string        { return "CronCreate" }
func (t *CronCreateTool) Description() string { return "Schedule a prompt to run on a cron schedule" }
func (t *CronCreateTool) Icon() string        { return "clock" }

func (t *CronCreateTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	cronExpr := tool.GetString(params, "cron")
	if cronExpr == "" {
		return toolresult.NewErrorResult(t.Name(), "cron expression is required")
	}

	prompt := tool.GetString(params, "prompt")
	if prompt == "" {
		return toolresult.NewErrorResult(t.Name(), "prompt is required")
	}

	// recurring defaults to true if not specified
	recurring := true
	if v, ok := params["recurring"].(bool); ok {
		recurring = v
	}

	durable := tool.GetBool(params, "durable")

	job, err := cron.Default().Create(cronExpr, prompt, recurring, durable)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	desc := cron.Describe(cronExpr)
	mode := "recurring"
	if !recurring {
		mode = "one-shot"
	}

	output := fmt.Sprintf("Scheduled %s job %s (%s). Next fire: %s",
		mode, job.ID, desc, job.NextFire.Format("15:04:05"))
	if recurring {
		output += fmt.Sprintf(". Auto-expires: %s", job.ExpiresAt.Format("2006-01-02 15:04"))
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  output,
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("Job %s: %s", job.ID, desc),
		},
	}
}

func init() {
	tool.Register(&CronCreateTool{})
}
