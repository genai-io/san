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

// EditTool performs exact, batched string replacements on files.
type EditTool struct{}

type editReplacement struct {
	oldString string
	newString string
}

type editRange struct {
	start       int
	end         int
	replacement string
	editIndex   int
}

func (t *EditTool) Name() string        { return "Edit" }
func (t *EditTool) Description() string { return "Edit file contents using exact string replacements" }
func (t *EditTool) Icon() string        { return IconEdit }

func (t *EditTool) RequiresPermission() bool { return true }

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
	newContent, err := applyEdits(string(content), edits)
	if err != nil {
		return nil, &tool.ToolError{Message: err.Error()}
	}

	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		FilePath:    filePath,
		Description: fmt.Sprintf("Apply %d replacements", len(edits)),
		DiffMeta:    perm.GenerateDiff(filePath, string(content), newContent),
	}, nil
}

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
	newContent, err := applyEdits(oldContent, edits)
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
	output := fmt.Sprintf("Edited %s (%d replacements, +%d -%d)", filePath, len(edits), changes.AddedCount, changes.RemovedCount)
	return toolresult.ToolResult{
		Success: true,
		Output:  output,
		Details: toolresult.EditDetails{
			Path:         filePath,
			EditCount:    len(edits),
			AddedLines:   changes.AddedCount,
			RemovedLines: changes.RemovedCount,
			UnifiedDiff:  changes.UnifiedDiff,
		},
		HookResponse: map[string]any{
			"filePath":        filePath,
			"originalFile":    oldContent,
			"structuredPatch": []any{},
			"userModified":    false,
			"editResult": map[string]any{
				"path":         filePath,
				"editCount":    len(edits),
				"addedLines":   changes.AddedCount,
				"removedLines": changes.RemovedCount,
				"unifiedDiff":  changes.UnifiedDiff,
			},
		},
		Metadata: toolresult.ResultMetadata{Title: t.Name(), Icon: t.Icon(), Subtitle: filePath, Duration: time.Since(start)},
	}
}

func parseEditRequest(params map[string]any) (string, []editReplacement, error) {
	filePath, err := tool.RequireString(params, "path")
	if err != nil {
		return "", nil, err
	}
	rawEdits, ok := params["edits"]
	if !ok {
		return "", nil, &tool.ToolError{Message: "edits is required"}
	}
	items, ok := rawEdits.([]any)
	if !ok || len(items) == 0 {
		return "", nil, &tool.ToolError{Message: "edits must be a non-empty array"}
	}

	edits := make([]editReplacement, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return "", nil, &tool.ToolError{Message: fmt.Sprintf("edits[%d] must be an object", i)}
		}
		oldString, ok := item["oldText"].(string)
		if !ok || oldString == "" {
			return "", nil, &tool.ToolError{Message: fmt.Sprintf("edits[%d].oldText must be a non-empty string", i)}
		}
		newString, ok := item["newText"].(string)
		if !ok {
			return "", nil, &tool.ToolError{Message: fmt.Sprintf("edits[%d].newText must be a string", i)}
		}
		oldString = normalizeLineEndings(oldString)
		newString = normalizeLineEndings(newString)
		if _, duplicate := seen[oldString]; duplicate {
			return "", nil, &tool.ToolError{Message: fmt.Sprintf("edits[%d].oldText duplicates another edit", i)}
		}
		seen[oldString] = struct{}{}
		edits = append(edits, editReplacement{oldString: oldString, newString: newString})
	}
	return filePath, edits, nil
}

func applyEdits(content string, edits []editReplacement) (string, error) {
	bom := ""
	if strings.HasPrefix(content, "\ufeff") {
		bom, content = "\ufeff", strings.TrimPrefix(content, "\ufeff")
	}
	windowsLineEndings := strings.Contains(content, "\r\n")
	if windowsLineEndings && strings.Contains(strings.ReplaceAll(content, "\r\n", ""), "\n") {
		return "", fmt.Errorf("file has mixed line endings; normalize it before editing")
	}
	content = normalizeLineEndings(content)

	ranges := make([]editRange, 0, len(edits))
	for i, edit := range edits {
		matches := editMatches(content, edit.oldString)
		switch len(matches) {
		case 0:
			return "", fmt.Errorf("edits[%d]: oldText was not found; re-read the file and provide exact current text", i)
		case 1:
			start := matches[0]
			ranges = append(ranges, editRange{start: start, end: start + len(edit.oldString), replacement: edit.newString, editIndex: i})
		default:
			return "", fmt.Errorf("edits[%d]: oldText matches %d locations; include more surrounding context", i, len(matches))
		}
	}

	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	for i := 1; i < len(ranges); i++ {
		if ranges[i].start < ranges[i-1].end {
			return "", fmt.Errorf("edits[%d] overlaps edits[%d]; combine them into one edit", ranges[i-1].editIndex, ranges[i].editIndex)
		}
	}
	for i := len(ranges) - 1; i >= 0; i-- {
		content = content[:ranges[i].start] + ranges[i].replacement + content[ranges[i].end:]
	}
	if windowsLineEndings {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	return bom + content, nil
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

func normalizeLineEndings(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

func resolveEditPath(filePath, cwd string) string {
	if filepath.IsAbs(filePath) {
		return filePath
	}
	return filepath.Join(cwd, filePath)
}

func (t *EditTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.ExecuteApproved(ctx, params, cwd)
}

func init() {
	tool.Register(&EditTool{})
}
