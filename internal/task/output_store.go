package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	outputDirMu sync.RWMutex
	outputDir   string
)

// SetOutputDir configures the directory used for stable task output files.
// It delegates to the internal implementation, keeping backward compatibility
// for callers that call task.SetOutputDir() directly.
func SetOutputDir(dir string) error {
	return setOutputDir(dir)
}

// setOutputDir is the internal implementation.
func setOutputDir(dir string) error {
	outputDirMu.Lock()
	defer outputDirMu.Unlock()

	outputDir = dir
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// OutputPath returns the stable output file path for a task ID.
func OutputPath(taskID string) string {
	outputDirMu.RLock()
	dir := outputDir
	outputDirMu.RUnlock()
	if dir == "" || taskID == "" {
		return ""
	}
	return filepath.Join(dir, taskID+".log")
}

func initOutputFile(taskID string) string {
	path := OutputPath(taskID)
	if path == "" {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return ""
	}
	_ = f.Close()
	return path
}

type outputRecord struct {
	Timestamp   string         `json:"timestamp"`
	Event       string         `json:"event"`
	TaskType    string         `json:"task_type,omitempty"`
	Description string         `json:"description,omitempty"`
	Status      string         `json:"status,omitempty"`
	Content     string         `json:"content,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

func appendOutputFile(path string, record outputRecord) {
	if path == "" || record.Event == "" {
		return
	}
	record.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(data)
}
