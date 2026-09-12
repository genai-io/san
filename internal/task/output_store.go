package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func initOutputFile(path string) string {
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
