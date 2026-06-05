package history

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/confdir"
)

const maxHistoryEntries = 500

func historyFilePath(cwd string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(confdir.Dir(cwd), "history")
	}
	encoded := strings.ReplaceAll(strings.TrimSuffix(cwd, "/"), "/", "-")
	return filepath.Join(confdir.Dir(homeDir), "projects", encoded, "history")
}

func escapeEntry(entry string) string {
	entry = strings.ReplaceAll(entry, "\\", "\\\\")
	return strings.ReplaceAll(entry, "\n", "\\n")
}

func unescapeEntry(line string) string {
	line = strings.ReplaceAll(line, "\\\\", "\x00")
	line = strings.ReplaceAll(line, "\\n", "\n")
	return strings.ReplaceAll(line, "\x00", "\\")
}

func truncate(entries []string) []string {
	if len(entries) > maxHistoryEntries {
		return entries[len(entries)-maxHistoryEntries:]
	}
	return entries
}

func Load(cwd string) []string {
	f, err := os.Open(historyFilePath(cwd))
	if err != nil {
		return nil
	}
	defer f.Close()

	var history []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024) // 256KB max line
	for scanner.Scan() {
		if entry := unescapeEntry(scanner.Text()); entry != "" {
			history = append(history, entry)
		}
	}
	// Partial history is better than none — ignore scanner errors
	return truncate(history)
}

func Save(cwd string, history []string) {
	path := historyFilePath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// Write to temp file + rename for atomic replacement (prevents data loss on crash)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	for _, entry := range truncate(history) {
		_, _ = fmt.Fprintln(w, escapeEntry(entry))
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return
	}
	f.Close()
	_ = os.Rename(tmp, path)
}
