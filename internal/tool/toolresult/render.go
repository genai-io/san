package toolresult

import (
	"fmt"
	"strings"
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

// BashDetails preserves failure metadata separately from the model-facing text.
// The UI uses it to show the process error in the summary without counting or
// repeating the appended "Error: ..." line as command output.
type BashDetails struct {
	Error     string `json:"error"`
	LineCount int    `json:"lineCount"`
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
