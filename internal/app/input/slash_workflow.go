package input

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/tool"
)

// workflowRunner is what /workflow needs from the Workflow tool.
type workflowRunner interface {
	Saved() string
	Launch(name string, inputs map[string]string) (bounds, taskID string, err error)
}

// handleWorkflowCommand runs a saved workflow directly. The user has already
// named what to run, so there is nothing for a model turn to decide — routing
// it through one would only add latency and a chance to get the call wrong.
// Launch applies the same checks and starts the same background task as a
// model's call; and because a tool the model is not offered is still
// registered, this works with the tool disabled.
func (c *SlashCommandController) handleWorkflowCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	t, _ := c.env.ToolSvc.Get(tool.ToolWorkflow)
	wt, ok := t.(workflowRunner)
	if !ok {
		return "", nil, errors.New("the Workflow tool is not available in this build")
	}
	fields, err := splitQuoted(args)
	if err != nil {
		return "", nil, err
	}
	if len(fields) == 0 {
		list := wt.Saved()
		if list == "" {
			return "No saved workflows. Add one as .san/workflows/<name>.md.", nil, nil
		}
		return "Saved workflows:" + list + "\n\nRun one with /workflow <name> [key=value …].", nil, nil
	}

	inputs := map[string]string{}
	for _, kv := range fields[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return "", nil, fmt.Errorf("input %q is not key=value", kv)
		}
		inputs[k] = v
	}
	bounds, id, err := wt.Launch(fields[0], inputs)
	if err != nil {
		return "", nil, err
	}
	return bounds + "\nTask ID: " + id, nil, nil
}

// splitQuoted splits args on whitespace, keeping a run inside single or double
// quotes together (quotes dropped), so key="two words" is one field.
func splitQuoted(args string) ([]string, error) {
	var fields []string
	var cur strings.Builder
	var quote rune
	inField := false
	for _, r := range args {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote, inField = r, true
		case unicode.IsSpace(r):
			if inField {
				fields = append(fields, cur.String())
				cur.Reset()
				inField = false
			}
		default:
			cur.WriteRune(r)
			inField = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed %c quote", quote)
	}
	if inField {
		fields = append(fields, cur.String())
	}
	return fields, nil
}
