package task

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/genai-io/san/internal/core"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

// Tools exposes only this pool. Hosts apply their normal permission gate and
// disabled-tool policy; no model-supplied owner or global task lookup is used.
func (m *Manager) Tools() []core.Tool {
	return []core.Tool{
		sdkagent.ToolFunc("TaskOutput", "Read background task status and collected output. Omit task_id to list this session's tasks. Tasks live only for this connection.", func(ctx context.Context, input struct {
			TaskID string `json:"task_id,omitempty"`
		}) (sdkagent.Result, error) {
			if err := ctx.Err(); err != nil {
				return sdkagent.Result{}, err
			}
			if input.TaskID == "" {
				var lines []string
				for _, t := range m.List() {
					info := t.GetStatus()
					lines = append(lines, fmt.Sprintf("%s [%s] %s", info.ID, info.Status, info.Description))
				}
				slices.Sort(lines)
				return sdkagent.TextResult(strings.Join(lines, "\n")), nil
			}
			t, ok := m.Get(input.TaskID)
			if !ok {
				return sdkagent.Result{}, fmt.Errorf("task not found in this session")
			}
			data, err := json.Marshal(t.GetStatus())
			return sdkagent.TextResult(string(data)), err
		}),
		sdkagent.ToolFunc("TaskStop", "Cancel a background task owned by this session. Cancellation requests termination; TaskOutput reports its status and collected output.", func(ctx context.Context, input struct {
			TaskID string `json:"task_id"`
		}) (sdkagent.Result, error) {
			if err := ctx.Err(); err != nil {
				return sdkagent.Result{}, err
			}
			t, ok := m.Get(input.TaskID)
			if !ok {
				return sdkagent.Result{}, fmt.Errorf("task not found in this session")
			}
			if t.IsRunning() {
				if err := t.Kill(); err != nil {
					return sdkagent.Result{}, err
				}
			}
			return sdkagent.TextResult("Cancellation requested for " + input.TaskID), nil
		}),
	}
}
