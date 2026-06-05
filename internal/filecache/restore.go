package filecache

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/genai-io/san/v2/internal/log"
)

type RestoredFile struct {
	FilePath string
	Content  string
	Lines    int
}

func (c *Cache) RestoreRecent() ([]RestoredFile, int) {
	entries := c.Recent(restoreMaxFiles)
	if len(entries) == 0 {
		return nil, 0
	}

	var files []RestoredFile
	totalTokens := 0

	for _, entry := range entries {
		content, lines, tokens := readFileForRestore(entry.FilePath)
		if content == "" {
			continue
		}
		if totalTokens+tokens > restoreMaxTotal {
			if totalTokens == 0 {
				content, lines, tokens = truncateToTokenBudget(content, restoreMaxTotal)
			} else {
				break
			}
		}
		files = append(files, RestoredFile{
			FilePath: entry.FilePath,
			Content:  content,
			Lines:    lines,
		})
		totalTokens += tokens
	}

	return files, totalTokens
}

func FormatRestoredFiles(files []RestoredFile) string {
	if len(files) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<post-compact-context>\n")
	sb.WriteString("The following files were recently accessed before compaction and are restored for context:\n\n")

	for _, f := range files {
		fmt.Fprintf(&sb, "<file path=%q lines=%d>\n", f.FilePath, f.Lines)
		sb.WriteString(f.Content)
		if !strings.HasSuffix(f.Content, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("</file>\n\n")
	}

	sb.WriteString("</post-compact-context>")
	return sb.String()
}

func readFileForRestore(filePath string) (content string, lines int, tokens int) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, 0
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return "", 0, 0
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), 1024*1024) // allow lines up to 1MB
	lineCount := 0
	charCount := 0

	for scanner.Scan() {
		lineCount++
		line := scanner.Text()
		charCount += len(line) + 1
		estimatedTokens := charCount / 4
		if estimatedTokens > restoreMaxPerFile {
			break
		}
		if lineCount > 1 {
			sb.WriteString("\n")
		}
		sb.WriteString(line)
	}

	if err := scanner.Err(); err != nil {
		log.Logger().Warn("scanner error reading file for restore",
			zap.String("path", filePath), zap.Error(err))
	}
	if lineCount == 0 {
		return "", 0, 0
	}

	text := sb.String()
	return text, lineCount, len(text) / 4
}

func truncateToTokenBudget(content string, maxTokens int) (string, int, int) {
	maxBytes := maxTokens * 4
	if len(content) <= maxBytes {
		lines := strings.Count(content, "\n") + 1
		return content, lines, len(content) / 4
	}
	// Snap to a valid UTF-8 boundary to avoid splitting multi-byte runes.
	for maxBytes > 0 && !utf8.RuneStart(content[maxBytes]) {
		maxBytes--
	}
	content = content[:maxBytes]
	if idx := strings.LastIndex(content, "\n"); idx > 0 {
		content = content[:idx]
	}
	lines := strings.Count(content, "\n") + 1
	return content, lines, len(content) / 4
}
