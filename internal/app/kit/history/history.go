package history

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/atomicfile"
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
	r := bufio.NewReaderSize(f, 256*1024)
	var oversized bool
	for {
		line, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			oversized = true
			continue
		}
		if err != nil && err != io.EOF {
			break
		}
		if !oversized {
			text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
			if text != "" {
				history = append(history, unescapeEntry(text))
			}
		}
		if err == io.EOF {
			break
		}
		oversized = false
	}
	return truncate(history)
}

func Save(cwd string, history []string) {
	var b strings.Builder
	for _, entry := range truncate(history) {
		b.WriteString(escapeEntry(entry))
		b.WriteByte('\n')
	}
	// Best-effort: losing shell history is not worth surfacing to the user.
	_ = atomicfile.Write(historyFilePath(cwd), []byte(b.String()), 0o644)
}
