package flow

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// nodeRunner is the NodeRunner seam: one node is one subagent turn
// through the same executor, and so the same permission gate, as Agent.
// Progress goes to the scheduler task's live tail only; the native child task
// owns the complete transcript.
type nodeRunner struct {
	blueprint      *Blueprint
	instructions   map[string]string
	onInstructions func(*Node, string) error
	main           MainRunExecutor
	exec           tool.AgentExecutor
	task           *task.AgentTask
	name           string
	usageMu        sync.Mutex
	steps          int
	tokens         int
}

func (r *nodeRunner) snapshot(id string) string {
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	return r.instructions[id]
}

func (r *nodeRunner) revisionInstructions(n *Node) string {
	if r.blueprint == nil {
		return ""
	}
	ancestors := r.blueprint.ancestors(n)
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	// Loop copies are ordered by round. The latest completed ancestor is
	// the draft actually available to this revision's critic and decision.
	for i := len(r.blueprint.Nodes) - 1; i >= 0; i-- {
		writer := r.blueprint.Nodes[i]
		if writer.Executor != "main" || !ancestors[writer.ID] {
			continue
		}
		if instructions, ok := r.instructions[writer.ID]; ok {
			return instructions
		}
	}
	return ""
}

func (r *nodeRunner) RunNode(ctx context.Context, c Call) (string, error) {
	n := c.Node
	if n.Executor == "main" {
		if r.main == nil {
			return "", fmt.Errorf("main executor not configured")
		}
		out, err := r.main.RunNode(ctx, c)
		if err != nil {
			return out, err
		}
		instructions := r.main.Instructions()
		r.usageMu.Lock()
		if r.instructions == nil {
			r.instructions = map[string]string{}
		}
		r.instructions[n.ID] = instructions
		r.usageMu.Unlock()
		if r.onInstructions != nil {
			if err := r.onInstructions(n, instructions); err != nil {
				return out, err
			}
		}
		return out, nil
	}
	if instructions := r.revisionInstructions(n); strings.TrimSpace(instructions) != "" {
		c.Prompt += "\n\nAccepted user amendments for the current writer revision:\n" + instructions
	}
	label := n.ID
	if c.Label != "" {
		label += "·" + c.Label
	}
	res, err := r.exec.Run(ctx, tool.AgentExecRequest{
		OnIdentity: func(identity toolresult.AgentIdentity) {
			if c.OnExecution != nil {
				c.OnExecution(Execution{TaskID: identity.ID, SessionID: identity.SessionID})
			}
		},
		Flow:             &tool.FlowOrigin{TaskID: r.task.GetID(), Name: r.name, NodeID: n.ID, WorkerLabel: c.Label},
		ParentTaskID:     r.task.GetID(),
		Agent:            n.Config["agent"],
		Prompt:           c.Prompt,
		Description:      label,
		Background:       true,
		Model:            n.Config["model"],
		Mode:             n.Config["mode"],
		ActivityMaxChars: 320,
		OnActivity: func(msg string) {
			r.task.AppendProgress(label + " ▸ " + msg)
		},
		OnToolStart: func(_, call string) {
			r.task.AppendProgress(label + " ▸ " + call)
		},
		OnToolResult: func(_, call, output string, toolErr error) {
			marker := "✓"
			if toolErr != nil {
				marker = "✗"
			}
			r.task.AppendProgress(label + " ▸ " + marker + " " + call + " · " + toolResultSummary(output, toolErr))
		},
	})
	output := ""
	if res != nil {
		if c.OnExecution != nil && (res.Identity.ID != "" || res.Identity.SessionID != "") {
			c.OnExecution(Execution{TaskID: res.Identity.ID, SessionID: res.Identity.SessionID})
		}
		output = res.Content
		r.usageMu.Lock()
		r.steps += res.StepCount
		r.tokens += res.TotalInputTokens + res.TotalOutputTokens
		r.task.UpdateProgress(r.steps, r.tokens)
		r.usageMu.Unlock()
		if err == nil && !res.Success {
			err = fmt.Errorf("%s", res.Error)
		}
	} else if err == nil {
		err = fmt.Errorf("agent returned no result")
	}
	if err != nil {
		if res != nil {
			partial := output
			if res.AgentID != "" {
				partial += "\n\nAgentID: " + res.AgentID
			}
			if res.OutputFile != "" {
				partial += "\n\nTranscript: " + res.OutputFile
			}
			if strings.TrimSpace(partial) != "" {
				r.task.AppendOutput([]byte(fmt.Sprintf("\n## %s (unfinished)\n%s\n", label, partial)))
			}
		}
		return output, err
	}
	if preview := resultPreview(res.Content); preview != "" {
		r.task.AppendProgress(label + " ▸ Result: " + preview)
	}
	return res.Content, nil
}
func toolResultSummary(output string, toolErr error) string {
	if toolErr != nil {
		return ansi.Truncate(strings.Join(strings.Fields(ansi.Strip(toolErr.Error())), " "), 140, "…")
	}
	if strings.TrimSpace(output) == "" || output == "(no output)" {
		return "no output"
	}
	lines := strings.Count(output, "\n")
	if !strings.HasSuffix(output, "\n") {
		lines++
	}
	head := output[:min(len(output), 4096)]
	first := ""
	for line := range strings.SplitSeq(head, "\n") {
		first = strings.Join(strings.Fields(ansi.Strip(line)), " ")
		if first != "" {
			break
		}
	}
	first = ansi.Truncate(first, 56, "…")
	if lines == 1 && len(output) <= 160 {
		return first
	}
	size := fmt.Sprintf("%d B", len(output))
	if len(output) >= 1024 {
		size = fmt.Sprintf("%.1f KiB", float64(len(output))/1024)
	}
	summary := fmt.Sprintf("%d lines · %s", lines, size)
	if first != "" {
		summary += " · " + first
	}
	return summary
}

func resultPreview(content string) string {
	text := []rune(strings.Join(strings.Fields(content), " "))
	if len(text) > 240 {
		return string(text[:239]) + "…"
	}
	return string(text)
}
