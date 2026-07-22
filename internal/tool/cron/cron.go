// Package cron exposes the Cron tool: scheduled prompts injected into the
// running session (create / delete / list). Disabled by default — the user
// enables it from the /tool panel.
package cron

import (
	"context"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/cron"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// CronTool manages scheduled prompts for the running session.
type CronTool struct{}

func (t *CronTool) Name() string        { return "Cron" }
func (t *CronTool) Description() string { return "Manage scheduled prompts (create, delete, list)" }
func (t *CronTool) Icon() string        { return "clock" }

func (t *CronTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	switch action := tool.GetString(params, "action"); action {
	case "create":
		return t.create(params)
	case "delete":
		return t.delete(params)
	case "list":
		return t.list()
	default:
		return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("action must be create, delete, or list; got %q", action))
	}
}

func (t *CronTool) create(params map[string]any) toolresult.ToolResult {
	cronExpr := tool.GetString(params, "cron")
	if cronExpr == "" {
		return toolresult.NewErrorResult(t.Name(), "cron expression is required for action create")
	}
	prompt := tool.GetString(params, "prompt")
	if prompt == "" {
		return toolresult.NewErrorResult(t.Name(), "prompt is required for action create")
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

func (t *CronTool) delete(params map[string]any) toolresult.ToolResult {
	id := tool.GetString(params, "id")
	if id == "" {
		return toolresult.NewErrorResult(t.Name(), "job id is required for action delete")
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

func (t *CronTool) list() toolresult.ToolResult {
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
	fmt.Fprintf(&sb, "%d scheduled job(s):\n\n", len(jobs))
	for _, j := range jobs {
		desc := cron.Describe(j.Cron)
		mode := "recurring"
		if !j.Recurring {
			mode = "one-shot"
		}
		scope := "session"
		if j.Durable {
			scope = "durable"
		}
		fmt.Fprintf(&sb, "%s  [%s, %s] %s — next fire %s\n  prompt: %s\n",
			j.ID, mode, scope, desc, j.NextFire.Format("2006-01-02 15:04:05"), j.Prompt)
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  strings.TrimRight(sb.String(), "\n"),
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("%d job(s)", len(jobs)),
		},
	}
}

func init() {
	tool.Register(&CronTool{})
}
