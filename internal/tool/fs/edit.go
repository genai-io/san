package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const IconEdit = "✏️"

// maxStoredDiffLines caps the unified diff persisted with a result so a huge
// rewrite doesn't bloat the session transcript; the UI shows the cap notice.
const maxStoredDiffLines = 400

// EditTool performs exact string replacement on a file. The parameter shape
// (file_path, old_string, new_string, replace_all) is the one models know
// from training, which matters more for call reliability than any schema
// documentation.
type EditTool struct{}

type editReplacement struct {
	oldString  string
	newString  string
	replaceAll bool
}

func (t *EditTool) Name() string        { return "Edit" }
func (t *EditTool) Description() string { return "Edit file contents using exact string replacement" }
func (t *EditTool) Icon() string        { return IconEdit }

func (t *EditTool) RequiresPermission() bool { return true }

func (t *EditTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	filePath, edit, err := parseEditRequest(params)
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
	view, err := viewOf(filePath)
	if err != nil {
		return nil, &tool.ToolError{Message: err.Error()}
	}
	if view == viewNone {
		return nil, &tool.ToolError{Message: filePath + " has not been read in this session; Read it before editing it"}
	}
	newContent, _, err := applyEdit(string(content), edit)
	if err != nil {
		return nil, &tool.ToolError{Message: err.Error()}
	}

	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		FilePath:    filePath,
		Description: "Replace text in file",
		DiffMeta:    perm.GenerateDiff(filePath, string(content), newContent),
	}, nil
}

func (t *EditTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()
	filePath, edit, err := parseEditRequest(params)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}
	filePath = resolveEditPath(filePath, cwd)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), "failed to read file: "+err.Error())
	}
	view, err := viewOf(filePath)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}
	if view == viewNone {
		return toolresult.NewErrorResult(t.Name(), filePath+" has not been read in this session; Read it before editing it")
	}

	oldContent := string(content)
	newContent, replaceCount, err := applyEdit(oldContent, edit)
	if err != nil {
		// A stale view turns a match failure ambiguous: old_string may be
		// wrong, or the target may have been changed under the model. Name
		// the staleness so the recovery (re-read) is obvious.
		if view == viewStale {
			return toolresult.NewErrorResult(t.Name(),
				fmt.Sprintf("%s changed on disk after it was last read, and against its current content: %s. Read the file again", filePath, err.Error()))
		}
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	mode := os.FileMode(0o644)
	if info, err := os.Stat(filePath); err == nil {
		mode = info.Mode()
	}
	if err := os.WriteFile(filePath, []byte(newContent), mode); err != nil {
		return toolresult.NewErrorResult(t.Name(), "failed to write file: "+err.Error())
	}
	recordFileWritten(filePath)

	changes := perm.GenerateDiff(filePath, oldContent, newContent)
	output := fmt.Sprintf("Edited %s (%d replacement(s), +%d -%d)", filePath, replaceCount, changes.AddedCount, changes.RemovedCount)
	// On the fresh path the hint suppresses the verify-read reflex; on the
	// stale path the edit landed cleanly but the file holds other changes
	// the model has not seen.
	if view == viewStale {
		output += ". Note: the file had changed on disk since your last read — this edit applied cleanly, but the file contains other changes not in your context; Read it before edits that depend on surrounding content"
	} else {
		output += "; file state is current — no need to re-read"
	}
	return toolresult.ToolResult{
		Success: true,
		Output:  output,
		Details: toolresult.FileChangeDetails{
			Path:         filePath,
			EditCount:    replaceCount,
			AddedLines:   changes.AddedCount,
			RemovedLines: changes.RemovedCount,
			UnifiedDiff:  perm.CapUnifiedDiff(changes.UnifiedDiff, maxStoredDiffLines),
		},
		HookResponse: map[string]any{
			"filePath":        filePath,
			"oldString":       edit.oldString,
			"newString":       edit.newString,
			"replaceAll":      edit.replaceAll,
			"originalFile":    oldContent,
			"structuredPatch": []any{},
			"userModified":    false,
		},
		Metadata: toolresult.ResultMetadata{Title: t.Name(), Icon: t.Icon(), Subtitle: filePath, Duration: time.Since(start)},
	}
}

func parseEditRequest(params map[string]any) (string, editReplacement, error) {
	filePath, err := tool.RequireString(params, "file_path")
	if err != nil {
		return "", editReplacement{}, err
	}
	oldString, ok := params["old_string"].(string)
	if !ok || oldString == "" {
		return "", editReplacement{}, &tool.ToolError{Message: "old_string must be a non-empty string"}
	}
	newString, ok := params["new_string"].(string)
	if !ok {
		return "", editReplacement{}, &tool.ToolError{Message: "new_string is required (use an empty string to delete old_string)"}
	}
	oldString = normalizeLineEndings(oldString)
	newString = normalizeLineEndings(newString)
	if newString == oldString {
		return "", editReplacement{}, &tool.ToolError{Message: "new_string must be different from old_string"}
	}
	return filePath, editReplacement{
		oldString:  oldString,
		newString:  newString,
		replaceAll: tool.GetBool(params, "replace_all"),
	}, nil
}

// applyEdit performs the replacement on content, preserving a BOM and
// Windows line endings. It returns the new content and how many occurrences
// were replaced.
func applyEdit(content string, edit editReplacement) (string, int, error) {
	bom := ""
	if after, ok := strings.CutPrefix(content, "\ufeff"); ok {
		bom, content = "\ufeff", after
	}
	windowsLineEndings := strings.Contains(content, "\r\n")
	if windowsLineEndings && strings.Contains(strings.ReplaceAll(content, "\r\n", ""), "\n") {
		return "", 0, fmt.Errorf("file has mixed line endings; normalize it before editing")
	}
	content = normalizeLineEndings(content)

	content, replaceCount, err := replaceInContent(content, edit)
	if err != nil {
		return "", 0, err
	}

	if windowsLineEndings {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	return bom + content, replaceCount, nil
}

func replaceInContent(content string, edit editReplacement) (string, int, error) {
	occurrences := strings.Count(content, edit.oldString)
	if edit.replaceAll && occurrences > 0 {
		return strings.ReplaceAll(content, edit.oldString, edit.newString), occurrences, nil
	}

	switch occurrences {
	case 1:
		index := strings.Index(content, edit.oldString)
		return content[:index] + edit.newString + content[index+len(edit.oldString):], 1, nil
	case 0:
		// Zero exact matches usually means a whitespace transcription slip;
		// the tolerant ladder recovers or diagnoses it. This also serves
		// replace_all — a unique tolerant match is the only occurrence.
		match, err := resolveTolerantMatch(content, splitLineSpans(content), edit)
		if err != nil {
			return "", 0, err
		}
		return content[:match.start] + edit.newString + content[match.end:], 1, nil
	default:
		return "", 0, fmt.Errorf("old_string matches %d locations; add surrounding context to make it unique, or set replace_all to change every occurrence", occurrences)
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
