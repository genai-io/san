package fs

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const (
	IconWrite = "📝"
)

// WriteTool writes content to files
type WriteTool struct{}

func (t *WriteTool) Name() string        { return "Write" }
func (t *WriteTool) Description() string { return "Write content to a file" }
func (t *WriteTool) Icon() string        { return IconWrite }

// RequiresPermission returns true - Write always requires permission
func (t *WriteTool) RequiresPermission() bool {
	return true
}

// PreparePermission prepares a permission request with diff information
func (t *WriteTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	// Get parameters
	filePath, err := tool.RequireString(params, "file_path")
	if err != nil {
		return nil, err
	}

	content, ok := params["content"].(string)
	if !ok {
		return nil, &tool.ToolError{Message: "content is required"}
	}

	// Resolve relative path
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(cwd, filePath)
	}

	// Check if file exists
	var statErr error
	_, statErr = os.Stat(filePath)
	isNewFile := os.IsNotExist(statErr)
	if statErr != nil && !isNewFile {
		return nil, &tool.ToolError{Message: "failed to check file: " + statErr.Error()}
	}

	// Generate appropriate preview based on whether file exists
	var diffMeta *perm.DiffMetadata
	if isNewFile {
		// New file: use preview mode to show content directly
		diffMeta = perm.GeneratePreview(filePath, content, true)
	} else {
		// Overwriting: the model must hold a current view of what it destroys.
		if err := requireFreshRead(filePath); err != nil {
			return nil, &tool.ToolError{Message: err.Error()}
		}
		// Existing file: generate actual diff to show what will change
		oldContent, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return nil, &tool.ToolError{Message: "failed to read existing file: " + readErr.Error()}
		}
		diffMeta = perm.GenerateDiff(filePath, string(oldContent), content)
	}

	description := "Create new file"
	if !isNewFile {
		description = "Overwrite existing file"
	}

	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		FilePath:    filePath,
		Description: description,
		DiffMeta:    diffMeta,
	}, nil
}

// ExecuteApproved performs the file write after user approval
func (t *WriteTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()

	// Get parameters — validate file_path even after approval, since params
	// could differ from what PreparePermission validated.
	filePath, err := tool.RequireString(params, "file_path")
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}
	content, _ := params["content"].(string)

	// Resolve relative path
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(cwd, filePath)
	}

	// Create parent directories if needed
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return toolresult.NewErrorResult(t.Name(), "failed to create directory: "+err.Error())
	}

	// Check if file exists (for status message)
	_, statErr := os.Stat(filePath)
	isNewFile := os.IsNotExist(statErr)

	// Read the old content before overwriting: the freshness gate needs the
	// model to hold a current view, and the result carries a diff against it.
	oldContent := ""
	if !isNewFile {
		if err := requireFreshRead(filePath); err != nil {
			return toolresult.NewErrorResult(t.Name(), err.Error())
		}
		old, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return toolresult.NewErrorResult(t.Name(), "failed to read existing file: "+readErr.Error())
		}
		oldContent = string(old)
	}

	// Get optional mode parameter (default 0644).
	// Clamp to valid permission bits — JSON numbers are decimal, so an LLM
	// sending 755 would otherwise become octal 01363 (setgid + wrong perms).
	mode := os.FileMode(tool.GetInt(params, "mode", 0o644)) & 0o7777

	// Write file
	if err := os.WriteFile(filePath, []byte(content), mode); err != nil {
		return toolresult.NewErrorResult(t.Name(), "failed to write file: "+err.Error())
	}
	recordFileWritten(filePath)

	duration := time.Since(start)

	action := "Created"
	if !isNewFile {
		action = "Updated"
	}

	// Count lines
	lineCount := 1
	for _, c := range content {
		if c == '\n' {
			lineCount++
		}
	}

	// Determine CC-compatible write type
	writeType := "create"
	var originalFile any // null for a new file, per the CC hook contract
	if !isNewFile {
		writeType = "update"
		originalFile = oldContent
	}

	changes := perm.GenerateDiff(filePath, oldContent, content)

	return toolresult.ToolResult{
		Success: true,
		Output:  action + " " + filePath + " (" + strconv.Itoa(lineCount) + " lines)",
		Details: toolresult.FileChangeDetails{
			Path:         filePath,
			IsNewFile:    isNewFile,
			AddedLines:   changes.AddedCount,
			RemovedLines: changes.RemovedCount,
			UnifiedDiff:  perm.CapUnifiedDiff(changes.UnifiedDiff, maxStoredDiffLines),
		},
		HookResponse: map[string]any{
			"type":            writeType,
			"filePath":        filePath,
			"content":         content,
			"structuredPatch": []any{},
			"originalFile":    originalFile,
		},
		Metadata: toolresult.ResultMetadata{
			Title:     t.Name(),
			Icon:      t.Icon(),
			Subtitle:  filePath,
			LineCount: lineCount,
			Duration:  duration,
		},
	}
}

// Execute implements the Tool interface (for permission-unaware execution)
func (t *WriteTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	// This will be called if permission flow is bypassed
	return t.ExecuteApproved(ctx, params, cwd)
}

func init() {
	tool.Register(&WriteTool{})
}
