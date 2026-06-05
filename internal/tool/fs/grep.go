package fs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/toolresult"
)

// GrepTool searches for patterns in files using ripgrep.
type GrepTool struct{}

func (t *GrepTool) Name() string        { return "Grep" }
func (t *GrepTool) Description() string { return "Search for patterns in files" }
func (t *GrepTool) Icon() string        { return toolresult.IconGrep }

func (t *GrepTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()

	pattern, err := tool.RequireString(params, "pattern")
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	// Output mode: "content" | "files_with_matches" (default) | "count"
	outputMode := "files_with_matches"
	if om := tool.GetString(params, "output_mode"); om != "" {
		outputMode = om
	}

	headLimit := tool.GetInt(params, "head_limit", 250)
	offset := tool.GetInt(params, "offset", 0)

	// Build rg args
	args := []string{"--no-messages"}

	// Case sensitivity (-i param overrides case_sensitive)
	caseInsensitive := !tool.GetBool(params, "case_sensitive")
	if v, ok := params["-i"].(bool); ok {
		caseInsensitive = v
	}
	if caseInsensitive {
		args = append(args, "--ignore-case")
	}

	if tool.GetBool(params, "multiline") {
		args = append(args, "--multiline", "--multiline-dotall")
	}

	// Output mode flags
	switch outputMode {
	case "files_with_matches":
		args = append(args, "--files-with-matches")
	case "count":
		args = append(args, "--count")
	default: // "content"
		args = append(args, "--line-number", "--with-filename", "--no-heading")

		// Context lines
		contextLines := tool.GetInt(params, "context", tool.GetInt(params, "-C", 0))
		if contextLines > 0 {
			args = append(args, fmt.Sprintf("--context=%d", contextLines))
		} else {
			if a := tool.GetInt(params, "-A", 0); a > 0 {
				args = append(args, fmt.Sprintf("--after-context=%d", a))
			}
			if b := tool.GetInt(params, "-B", 0); b > 0 {
				args = append(args, fmt.Sprintf("--before-context=%d", b))
			}
		}
	}

	if fileType := tool.GetString(params, "type"); fileType != "" {
		args = append(args, "--type", fileType)
	}

	if glob := tool.GetString(params, "glob"); glob != "" {
		args = append(args, "--glob", glob)
	} else if include := tool.GetString(params, "include"); include != "" {
		args = append(args, "--glob", include)
	}

	// Pattern
	args = append(args, "--", pattern)

	searchPath := cwd
	if path := tool.GetString(params, "path"); path != "" {
		if filepath.IsAbs(path) {
			searchPath = path
		} else {
			searchPath = filepath.Join(cwd, path)
		}
	}
	args = append(args, searchPath)

	// Execute rg
	rgPath := findRG()
	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	// rg exits 1 when no matches found (not an error), 2 on actual error
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 2 {
				return toolresult.NewErrorResult(t.Name(), "search error: "+stderr.String())
			}
		}
	}

	duration := time.Since(start)

	// Parse output, applying offset and head_limit
	rawLines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(rawLines) == 1 && rawLines[0] == "" {
		rawLines = nil
	}

	// Apply offset
	if offset > 0 && offset < len(rawLines) {
		rawLines = rawLines[offset:]
	} else if offset >= len(rawLines) {
		rawLines = nil
	}

	truncated := false
	if headLimit > 0 && len(rawLines) > headLimit {
		rawLines = rawLines[:headLimit]
		truncated = true
	}

	// Build content lines for UI
	var lines []toolresult.ContentLine
	for _, line := range rawLines {
		lines = append(lines, toolresult.ContentLine{Text: line, Type: toolresult.LineMatch})
	}

	subtitle := fmt.Sprintf("pattern: %q mode: %s", pattern, outputMode)

	hookContent := strings.Join(rawLines, "\n")
	if truncated {
		hookContent += "\n(results truncated)"
	}

	return toolresult.ToolResult{
		Success: true,
		Lines:   lines,
		HookResponse: map[string]any{
			"mode":     outputMode,
			"numLines": len(rawLines),
			"content":  hookContent,
		},
		Metadata: toolresult.ResultMetadata{
			Title:     t.Name(),
			Icon:      t.Icon(),
			Subtitle:  subtitle,
			ItemCount: len(rawLines),
			Duration:  duration,
			Truncated: truncated,
		},
	}
}

// findRG returns the path to the rg binary, preferring PATH over the bundled vendor binary.
func findRG() string {
	if path, err := exec.LookPath("rg"); err == nil {
		return path
	}
	return "rg"
}

func init() {
	tool.Register(&GrepTool{})
}
