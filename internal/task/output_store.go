package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func initOutputFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// usableOutputPath never advertises an incomplete log as the full result.
func usableOutputPath(path, outputErr string) string {
	if outputErr != "" {
		return ""
	}
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

func appendOutputFile(path string, record outputRecord) error {
	if path == "" || record.Event == "" {
		return nil
	}
	record.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
