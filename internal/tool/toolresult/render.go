package toolresult

import (
	"fmt"
	"strconv"
	"strings"
)

// ToolResult represents the result of a tool execution
// EditDetails describes an applied Edit result for UI rendering.
type EditDetails struct {
	Path             string
	EditCount        int
	AddedLines       int
	RemovedLines     int
	UnifiedDiff      string
	FirstChangedLine int
}

type ToolResult struct {
	Success      bool             // Whether the tool succeeded
	Output       string           // Main output content
	Error        string           // Error message if failed
	Metadata     ResultMetadata   // Result metadata
	Lines        []ContentLine    // Formatted content lines (optional)
	Files        []string         // File list (for Glob)
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
				fmt.Fprintf(&sb, "%6d\t%s\n", line.LineNo, line.Text)
			}
		} else if r.Output != "" {
			sb.WriteString(r.Output)
		}
	case "Glob":
		if len(r.Files) > 0 {
			sb.Grow(len(r.Files) * 40)
			for _, f := range r.Files {
				sb.WriteString(f)
				sb.WriteString("\n")
			}
		} else if r.Output != "" {
			sb.WriteString(r.Output)
		}
	case "Grep":
		if len(r.Lines) > 0 {
			sb.Grow(len(r.Lines) * 60)
			for _, line := range r.Lines {
				if line.File != "" {
					sb.WriteString(line.File)
					sb.WriteString(":")
				}
				if line.LineNo > 0 {
					sb.WriteString(strconv.Itoa(line.LineNo))
					sb.WriteString(":")
				}
				sb.WriteString(line.Text)
				sb.WriteString("\n")
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
