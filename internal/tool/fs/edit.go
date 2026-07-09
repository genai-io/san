package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const (
	IconEdit = "✏️"
)

// EditTool performs string replacement edits on files
type EditTool struct{}

func (t *EditTool) Name() string        { return "Edit" }
func (t *EditTool) Description() string { return "Edit file contents using string replacement" }
func (t *EditTool) Icon() string        { return IconEdit }

// RequiresPermission returns true - Edit always requires permission
func (t *EditTool) RequiresPermission() bool {
	return true
}

// PreparePermission prepares a permission request with diff information
func (t *EditTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	// Get parameters
	filePath, err := tool.RequireString(params, "file_path")
	if err != nil {
		return nil, err
	}

	// old_string may be empty (inserting at start), so we only check presence not value
	oldString, ok := params["old_string"].(string)
	if !ok {
		return nil, &tool.ToolError{Message: "old_string is required"}
	}

	newString, ok := params["new_string"].(string)
	if !ok {
		return nil, &tool.ToolError{Message: "new_string is required"}
	}

	// Resolve relative path
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(cwd, filePath)
	}

	// Read current file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &tool.ToolError{Message: "file not found: " + filePath}
		}
		return nil, &tool.ToolError{Message: "failed to read file: " + err.Error()}
	}

	oldContent := string(content)

	// Check if old_string exists in the file
	count := strings.Count(oldContent, oldString)
	if count == 0 {
		return nil, &tool.ToolError{Message: "old_string not found in file"}
	}

	replaceAll := tool.GetBool(params, "replace_all")

	// If not replace_all, check uniqueness
	if !replaceAll && count > 1 {
		return nil, &tool.ToolError{Message: "old_string is not unique in file (found " + strconv.Itoa(count) + " occurrences). Use replace_all=true to replace all."}
	}

	// Calculate new content
	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(oldContent, oldString, newString)
	} else {
		newContent = strings.Replace(oldContent, oldString, newString, 1)
	}

	// Generate diff
	diffMeta := perm.GenerateDiff(filePath, oldContent, newContent)

	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		FilePath:    filePath,
		Description: "Replace text in file",
		DiffMeta:    diffMeta,
	}, nil
}

// ExecuteApproved performs the file edit after user approval
func (t *EditTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()

	// Get parameters
	filePath := tool.GetString(params, "file_path")
	oldString, _ := params["old_string"].(string)
	newString, _ := params["new_string"].(string)

	// Resolve relative path
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(cwd, filePath)
	}

	// Read current content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), "failed to read file: "+err.Error())
	}

	oldContent := string(content)

	replaceAll := tool.GetBool(params, "replace_all")

	// Verify old_string still exists in the current file content.
	occurrences := strings.Count(oldContent, oldString)
	if occurrences == 0 {
		return toolresult.NewErrorResult(t.Name(), "old_string not found in current file. Re-read the target lines and retry with exact current text.")
	}

	// When not replacing all, verify the string is still unique to avoid
	// applying the edit to a different location than what the user approved.
	if !replaceAll && occurrences > 1 {
		return toolresult.NewErrorResult(t.Name(),
			fmt.Sprintf("old_string is not unique in current file (%d occurrences found). Provide more surrounding context or use replace_all=true.", occurrences))
	}

	// Perform replacement
	var newContent string
	var replaceCount int
	if replaceAll {
		replaceCount = occurrences
		newContent = strings.ReplaceAll(oldContent, oldString, newString)
	} else {
		replaceCount = 1
		newContent = strings.Replace(oldContent, oldString, newString, 1)
	}

	// Preserve original file permissions
	mode := os.FileMode(0o644)
	if info, err := os.Stat(filePath); err == nil {
		mode = info.Mode()
	}

	// Write back to file
	if err := os.WriteFile(filePath, []byte(newContent), mode); err != nil {
		return toolresult.NewErrorResult(t.Name(), "failed to write file: "+err.Error())
	}

	duration := time.Since(start)
	output := "Edited " + filePath
	if replaceCount > 1 {
		output += " (" + strconv.Itoa(replaceCount) + " replacements)"
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  output,
		HookResponse: map[string]any{
			"filePath":        filePath,
			"oldString":       oldString,
			"newString":       newString,
			"originalFile":    oldContent,
			"structuredPatch": []any{},
			"userModified":    false,
			"replaceAll":      replaceAll,
		},
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: filePath,
			Duration: duration,
		},
	}
}

// Execute implements the Tool interface (for permission-unaware execution)
func (t *EditTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	// This will be called if permission flow is bypassed
	// For safety, we still perform the edit but without permission check
	return t.ExecuteApproved(ctx, params, cwd)
}

func init() {
	tool.Register(&EditTool{})
}
