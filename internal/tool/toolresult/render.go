package toolresult

import (
	"fmt"
	"strings"
	"time"
)

// FileChangeDetails describes an applied Edit or Write for UI rendering.
type FileChangeDetails struct {
	Path               string
	EditCount          int  // number of replacements (Edit only; 0 for Write)
	IsNewFile          bool // Write created the file rather than overwriting it
	AddedLines         int
	RemovedLines       int
	UnifiedDiff        string
	TruncatedDiffLines int // diff lines dropped by the storage cap (0 = complete)
}

// LastLines is the end of s, at most n lines; only that end is scanned, as an
// output may run to thousands.
func LastLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	end := strings.TrimSuffix(s, "\n")
	start := len(end)
	for range n {
		i := strings.LastIndexByte(end[:start], '\n')
		if i < 0 {
			return s
		}
		start = i
	}
	return s[start+1:]
}

// BashDetails is display metadata kept apart from the model-facing text, which
// may be cut short and lists stderr after stdout: the process error, the
// output's real line count, and — for a failure or a short output — its last
// lines as they interleaved, which the row keeps.
// A command started in the background has no output yet, only its task ID.
type BashDetails struct {
	Error     string `json:"error"`
	LineCount int    `json:"lineCount"`
	Tail      string `json:"tail,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
}

// AgentDetails keeps display metadata out of the parent model's result text.
type AgentDetails struct {
	Model        string
	ToolUses     int
	InputTokens  int
	OutputTokens int
	Duration     time.Duration
	Activity     []string
	Error        string
	Stopped      bool
	TaskID       string // set only for a background launch
}

// ToolResult represents the result of a tool execution
type ToolResult struct {
	Success      bool             // Whether the tool succeeded
	Output       string           // Main output content
	Error        string           // Error message if failed
	Metadata     ResultMetadata   // Result metadata
	Lines        []ContentLine    // Formatted content lines (optional)
	SkillInfo    *SkillResultInfo // Skill-specific info (for Skill tool)
	HookResponse any              // Structured response for PostToolUse hooks (CC-compatible)
	Details      any              // Structured result data for UI rendering
}

// NewErrorResult creates an error result
func NewErrorResult(title, errorMsg string) ToolResult {
	return ToolResult{
		Success: false,
		Error:   errorMsg,
		Metadata: ResultMetadata{
			Title: title,
		},
	}
}

// LineNumberFormat is the "line number, tab, content" shape of Read output.
// Edit's mismatch diagnostics echo file lines in the same format, so the two
// must stay one definition.
const LineNumberFormat = "%6d\t%s\n"

// FormatForLLM returns a plain text representation of the result for LLM consumption
func (r ToolResult) FormatForLLM() string {
	if !r.Success {
		if r.Output != "" {
			return r.Output + "\nError: " + r.Error
		}
		return "Error: " + r.Error
	}

	var sb strings.Builder

	switch r.Metadata.Title {
	case "Read":
		if len(r.Lines) > 0 {
			sb.Grow(len(r.Lines) * 40)
			for _, line := range r.Lines {
				fmt.Fprintf(&sb, LineNumberFormat, line.LineNo, line.Text)
			}
			// A trailing note (e.g. "output truncated at line N") must reach
			// the model along with the lines.
			if r.Output != "" {
				sb.WriteString(r.Output)
				sb.WriteByte('\n')
			}
		} else if r.Output != "" {
			sb.WriteString(r.Output)
		}
	default:
		if r.Output != "" {
			sb.WriteString(r.Output)
		}
	}

	return sb.String()
}
