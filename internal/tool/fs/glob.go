package fs

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const maxGlobResults = 100

// ignoredDirs are directories to skip during glob
var ignoredDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	".svn":         true,
	".hg":          true,
	"vendor":       true,
	"__pycache__":  true,
	".cache":       true,
	"dist":         true,
	"build":        true,
}

// GlobTool finds files matching a pattern
type GlobTool struct{}

func (t *GlobTool) Name() string        { return "Glob" }
func (t *GlobTool) Description() string { return "Find files matching a pattern" }
func (t *GlobTool) Icon() string        { return toolresult.IconGlob }

func (t *GlobTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()

	pattern, err := tool.RequireString(params, "pattern")
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	basePath := cwd
	if path := tool.GetString(params, "path"); path != "" {
		if filepath.IsAbs(path) {
			basePath = path
		} else {
			basePath = filepath.Join(cwd, path)
		}
	}

	// Check if base path exists
	if _, err := os.Stat(basePath); err != nil {
		if os.IsNotExist(err) {
			return toolresult.NewErrorResult(t.Name(), "path not found: "+basePath)
		}
		return toolresult.NewErrorResult(t.Name(), "failed to access path: "+err.Error())
	}

	// Collect matching files
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	var files []fileInfo

	// Walk the directory tree
	err = filepath.WalkDir(basePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Skip ignored directories
		if d.IsDir() {
			if ignoredDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative path for matching
		relPath, err := filepath.Rel(basePath, path)
		if err != nil {
			return nil
		}

		// Match pattern
		matched, err := doublestar.Match(pattern, relPath)
		if err != nil {
			return nil // Invalid pattern, skip
		}

		if matched {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			files = append(files, fileInfo{path: relPath, modTime: info.ModTime()})
		}

		return nil
	})

	if err != nil && err != context.Canceled {
		return toolresult.NewErrorResult(t.Name(), "glob error: "+err.Error())
	}

	// Sort by modification time (newest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	// Limit results
	truncated := false
	if len(files) > maxGlobResults {
		files = files[:maxGlobResults]
		truncated = true
	}

	// Build file list with absolute paths for hook response
	filePaths := make([]string, len(files))
	absFilePaths := make([]string, len(files))
	for i, f := range files {
		filePaths[i] = f.path
		absFilePaths[i] = filepath.Join(basePath, f.path)
	}

	duration := time.Since(start)

	// Build subtitle
	subtitle := pattern
	if basePath != cwd {
		relBase, err := filepath.Rel(cwd, basePath)
		if err == nil {
			subtitle = pattern + " in ./" + relBase
		} else {
			subtitle = pattern + " in " + basePath
		}
	}

	result := toolresult.ToolResult{
		Success: true,
		Files:   filePaths,
		HookResponse: map[string]any{
			"filenames":  absFilePaths,
			"durationMs": duration.Milliseconds(),
			"numFiles":   len(filePaths),
			"truncated":  truncated,
		},
		Metadata: toolresult.ResultMetadata{
			Title:     t.Name(),
			Icon:      t.Icon(),
			Subtitle:  subtitle,
			ItemCount: len(filePaths),
			Duration:  duration,
			Truncated: truncated,
		},
	}

	return result
}

func init() {
	tool.Register(&GlobTool{})
}
