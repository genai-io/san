package tool

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// ToolError represents a tool-specific validation or execution error.
// It is used by tools to signal structured failures during PreparePermission.
type ToolError struct {
	Message string
}

func (e *ToolError) Error() string {
	return e.Message
}

// GenerateRequestID generates a unique request ID using cryptographic randomness.
// This avoids collisions that could occur with time-based IDs in high-speed scenarios.
// BackgroundLaunchSuffix is what a tool appends when it starts work in the
// background: it is how San stops the model from narrating while the task
// runs. Shared because it is protocol, and two copies drift.
const BackgroundLaunchSuffix = "\n\nThe work is running in the background. You will be notified automatically when it completes.\nBriefly tell the user what you launched and end your response. Do not generate any other text — the result will arrive in a subsequent message."

func GenerateRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based if crypto/rand fails (unlikely)
		return "req_" + strconv.FormatInt(time.Now().UnixNano()%1000000, 10)
	}
	return "req_" + hex.EncodeToString(b)
}

// RequireString extracts a required string parameter from params.
// Returns a ToolError if the key is absent or the value is empty.
func RequireString(params map[string]any, key string) (string, error) {
	v, ok := params[key].(string)
	if !ok || v == "" {
		return "", &ToolError{Message: fmt.Sprintf("%s is required", key)}
	}
	return v, nil
}

// GetString extracts an optional string parameter. Returns "" if absent.
func GetString(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

// GetBool extracts an optional bool parameter. Returns false if absent.
func GetBool(params map[string]any, key string) bool {
	v, _ := params[key].(bool)
	return v
}

// GetFloat64 extracts a numeric parameter that may arrive as float64 or int
// (JSON numbers unmarshal to float64; some callers pass int directly).
// Returns defaultVal if the key is absent.
func GetFloat64(params map[string]any, key string, defaultVal float64) float64 {
	switch v := params[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return defaultVal
}

// GetInt is like GetFloat64 but returns an int.
func GetInt(params map[string]any, key string, defaultVal int) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return defaultVal
}
