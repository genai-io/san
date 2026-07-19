package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const IconEdit = "✏️"

// EditTool performs string replacement edits on files.
type EditTool struct{}

type editReplacement struct {
	oldString  string
	newString  string
	replaceAll bool
}

type editRange struct {
	start       int
	end         int
	replacement string
}

func (t *EditTool) Name() string        { return "Edit" }
func (t *EditTool) Description() string { return "Edit file contents using string replacement" }
func (t *EditTool) Icon() string        { return IconEdit }

// RequiresPermission returns true - Edit always requires permission.
func (t *EditTool) RequiresPermission() bool {
	return true
}

// PreparePermission prepares a permission request with diff information.
func (t *EditTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	filePath, edits, err := parseEditRequest(params)
	if err != nil {
		return nil, err
	}
	filePath = resolveEditPath(filePath, cwd)

	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &tool.ToolError{Message: "file not found: " + filePath}
		}
		return nil, &tool.ToolError{Message: "failed to read file: " + err.Error()}
	}
	oldContent := string(content)
	newContent, _, err := applyEdits(oldContent, edits)
	if err != nil {
		return nil, &tool.ToolError{Message: err.Error()}
	}

	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		FilePath:    filePath,
		Description: "Replace text in file",
		DiffMeta:    perm.GenerateDiff(filePath, oldContent, newContent),
	}, nil
}

// ExecuteApproved performs the file edit after user approval.
func (t *EditTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()
	filePath, edits, err := parseEditRequest(params)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}
	filePath = resolveEditPath(filePath, cwd)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), "failed to read file: "+err.Error())
	}
	oldContent := string(content)
	newContent, replaceCount, err := applyEdits(oldContent, edits)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	mode := os.FileMode(0o644)
	if info, err := os.Stat(filePath); err == nil {
		mode = info.Mode()
	}
	if err := os.WriteFile(filePath, []byte(newContent), mode); err != nil {
		return toolresult.NewErrorResult(t.Name(), "failed to write file: "+err.Error())
	}

	changes := perm.GenerateDiff(filePath, oldContent, newContent)
	output := fmt.Sprintf("Edited %s (+%d -%d", filePath, changes.AddedCount, changes.RemovedCount)
	if replaceCount > 1 {
		output += fmt.Sprintf(", %d replacements", replaceCount)
	}
	output += ")"

	hookResponse := map[string]any{
		"filePath":        filePath,
		"originalFile":    oldContent,
		"structuredPatch": []any{},
		"userModified":    false,
	}
	if len(edits) == 1 {
		hookResponse["oldString"] = edits[0].oldString
		hookResponse["newString"] = edits[0].newString
		hookResponse["replaceAll"] = edits[0].replaceAll
	} else {
		hookResponse["edits"] = editHookResponse(edits)
	}

	return toolresult.ToolResult{
		Success:      true,
		Output:       output,
		HookResponse: hookResponse,
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: filePath,
			Duration: time.Since(start),
		},
	}
}

func parseEditRequest(params map[string]any) (string, []editReplacement, error) {
	filePath, err := tool.RequireString(params, "file_path")
	if err != nil {
		return "", nil, err
	}
	if rawEdits, ok := params["edits"]; ok {
		if _, hasOld := params["old_string"]; hasOld {
			return "", nil, &tool.ToolError{Message: "edits cannot be combined with old_string or new_string"}
		}
		if _, hasNew := params["new_string"]; hasNew {
			return "", nil, &tool.ToolError{Message: "edits cannot be combined with old_string or new_string"}
		}
		edits, err := parseBatchEdits(rawEdits)
		return filePath, edits, err
	}

	oldString, ok := params["old_string"].(string)
	if !ok {
		return "", nil, &tool.ToolError{Message: "old_string is required"}
	}
	newString, ok := params["new_string"].(string)
	if !ok {
		return "", nil, &tool.ToolError{Message: "new_string is required"}
	}
	return filePath, []editReplacement{{oldString: oldString, newString: newString, replaceAll: tool.GetBool(params, "replace_all")}}, nil
}

func parseBatchEdits(raw any) ([]editReplacement, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, &tool.ToolError{Message: "edits must be a non-empty array"}
	}
	edits := make([]editReplacement, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, &tool.ToolError{Message: fmt.Sprintf("edits[%d] must be an object", i)}
		}
		oldString, ok := item["old_string"].(string)
		if !ok || oldString == "" {
			return nil, &tool.ToolError{Message: fmt.Sprintf("edits[%d].old_string must be a non-empty string", i)}
		}
		newString, ok := item["new_string"].(string)
		if !ok {
			return nil, &tool.ToolError{Message: fmt.Sprintf("edits[%d].new_string must be a string", i)}
		}
		if _, duplicate := seen[oldString]; duplicate {
			return nil, &tool.ToolError{Message: fmt.Sprintf("edits[%d].old_string duplicates another edit", i)}
		}
		seen[oldString] = struct{}{}
		edits = append(edits, editReplacement{oldString: oldString, newString: newString, replaceAll: tool.GetBool(item, "replace_all")})
	}
	return edits, nil
}

func applyEdits(content string, edits []editReplacement) (string, int, error) {
	if len(edits) == 1 && edits[0].oldString == "" {
		if edits[0].replaceAll {
			return strings.ReplaceAll(content, "", edits[0].newString), len(content) + 1, nil
		}
		return edits[0].newString + content, 1, nil
	}

	ranges := make([]editRange, 0, len(edits))
	for i, edit := range edits {
		matches := editMatches(content, edit.oldString)
		if len(matches) == 0 {
			return "", 0, fmt.Errorf("edits[%d]: old_string not found in current file", i)
		}
		if !edit.replaceAll && len(matches) > 1 {
			return "", 0, fmt.Errorf("edits[%d]: old_string is not unique in current file (%d occurrences found); provide more context or use replace_all=true", i, len(matches))
		}
		if !edit.replaceAll {
			matches = matches[:1]
		}
		for _, start := range matches {
			ranges = append(ranges, editRange{start: start, end: start + len(edit.oldString), replacement: edit.newString})
		}
	}

	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	for i := 1; i < len(ranges); i++ {
		if ranges[i].start < ranges[i-1].end {
			return "", 0, fmt.Errorf("edits overlap in current file")
		}
	}
	for i := len(ranges) - 1; i >= 0; i-- {
		content = content[:ranges[i].start] + ranges[i].replacement + content[ranges[i].end:]
	}
	return content, len(ranges), nil
}

func editMatches(content, oldString string) []int {
	var matches []int
	for offset := 0; ; {
		index := strings.Index(content[offset:], oldString)
		if index < 0 {
			return matches
		}
		index += offset
		matches = append(matches, index)
		offset = index + len(oldString)
	}
}

func editHookResponse(edits []editReplacement) []map[string]any {
	response := make([]map[string]any, len(edits))
	for i, edit := range edits {
		response[i] = map[string]any{
			"oldString":  edit.oldString,
			"newString":  edit.newString,
			"replaceAll": edit.replaceAll,
		}
	}
	return response
}

func resolveEditPath(filePath, cwd string) string {
	if filepath.IsAbs(filePath) {
		return filePath
	}
	return filepath.Join(cwd, filePath)
}

// Execute implements the Tool interface (for permission-unaware execution).
func (t *EditTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.ExecuteApproved(ctx, params, cwd)
}

func init() {
	tool.Register(&EditTool{})
}
