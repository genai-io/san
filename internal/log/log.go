package log

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/confdir"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	logger      *zap.Logger
	enabled     bool
	initialized bool
	mu          sync.Mutex
	turnCount   int // Track conversation turns

	devDir     string // DEV_DIR directory path for debug output
	devEnabled bool   // Whether DEV_DIR is enabled
)

// debugEnabled reports whether debug logging is on via SAN_DEBUG=1. Kept local
// because the log package is an infrastructure leaf and cannot import
// internal/setting.
func debugEnabled() bool {
	return os.Getenv("SAN_DEBUG") == "1"
}

// Init initializes the logger based on SAN_DEBUG env var
func Init() error {
	mu.Lock()
	defer mu.Unlock()

	if initialized {
		return nil
	}
	initialized = true

	// Initialize DEV_DIR for JSON debug output (independent of SAN_DEBUG)
	if dir := os.Getenv("DEV_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("failed to create DEV_DIR: %w", err)
		}
		devDir = dir
		devEnabled = true
	}

	if !debugEnabled() {
		logger = zap.NewNop()
		return nil
	}

	enabled = true

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Create log directory
	logDir := confdir.Dir(homeDir)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}

	logPath := filepath.Join(logDir, "debug.log")

	// Use lumberjack for log rotation
	writeSyncer := zapcore.AddSync(&lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    50, // MB
		MaxBackups: 3,
		MaxAge:     7, // Days
		Compress:   true,
	})

	// Console encoder for human-readable output
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "T",
		LevelKey:       "", // Hide level, we use custom markers
		NameKey:        "",
		CallerKey:      "", // Hide caller for cleaner output
		MessageKey:     "M",
		StacktraceKey:  "",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     zapcore.RFC3339TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
	}

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		writeSyncer,
		zapcore.DebugLevel,
	)

	logger = zap.New(core, zap.AddCaller())

	// Log initialization
	logger.Info("Debug logging started")

	return nil
}

// Logger returns the underlying zap logger
func Logger() *zap.Logger {
	if logger == nil {
		return zap.NewNop()
	}
	return logger
}

// Sync flushes any buffered log entries
func Sync() error {
	if logger != nil {
		return logger.Sync()
	}
	return nil
}

// nextTurn increments and returns the turn counter (main loop only)
func nextTurn() int {
	mu.Lock()
	defer mu.Unlock()
	turnCount++
	return turnCount
}

// currentTurn returns the current turn number (main loop only)
func currentTurn() int {
	mu.Lock()
	defer mu.Unlock()
	return turnCount
}

// getTurnPrefix returns the turn prefix for file naming (main loop only)
// Format: main-{turn}
// Example: main-005
func getTurnPrefix(turn int) string {
	return fmt.Sprintf("main-%03d", turn)
}

// AgentTurnTracker tracks turns for a specific agent loop.
// Each agent gets its own tracker, supporting parallel execution.
type AgentTurnTracker struct {
	parentPrefix string // e.g., "main-002" or "main-002:explore-003"
	agentName    string // e.g., "project-reviewer", "explore"
	turnCount    int
	mu           sync.Mutex
}

// NewAgentTurnTracker creates a tracker for an agent loop.
// agentName is the name of the agent (e.g., "project-reviewer").
// parentTracker is nil for first-level agents, or the parent's tracker for nested agents.
func NewAgentTurnTracker(agentName string, parentTracker *AgentTurnTracker) *AgentTurnTracker {
	mu.Lock()
	parentTurn := turnCount
	mu.Unlock()

	// Sanitize agent name for filename (replace special chars)
	safeName := sanitizeAgentName(agentName)

	var parentPrefix string
	if parentTracker != nil {
		// Nested agent: inherit parent's full prefix including current turn
		parentPrefix = fmt.Sprintf("%s:%s-%03d", parentTracker.parentPrefix, parentTracker.agentName, parentTracker.CurrentTurn())
	} else {
		// First-level agent: use main loop turn
		parentPrefix = fmt.Sprintf("main-%03d", parentTurn)
	}

	return &AgentTurnTracker{
		parentPrefix: parentPrefix,
		agentName:    safeName,
		turnCount:    0,
	}
}

// sanitizeAgentName makes agent name safe for filenames using an allowlist.
func sanitizeAgentName(name string) string {
	var result strings.Builder
	result.Grow(len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteRune('_')
		}
	}
	return result.String()
}

// NextTurn increments and returns the agent's turn counter
func (t *AgentTurnTracker) NextTurn() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turnCount++
	return t.turnCount
}

// CurrentTurn returns the agent's current turn number
func (t *AgentTurnTracker) CurrentTurn() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.turnCount
}

// GetTurnPrefix returns the turn prefix for file naming
// Format: {parentPrefix}:{agentName}-{turn}
// Examples:
//   - Main loop turn 5: "main-005" (use GetTurnPrefix directly)
//   - Agent "project-reviewer" spawned at main turn 5, sub-turn 3: "main-005:project-reviewer-003"
//   - Nested "explore" agent: "main-005:project-reviewer-003:explore-001"
func (t *AgentTurnTracker) GetTurnPrefix(turn int) string {
	return fmt.Sprintf("%s:%s-%03d", t.parentPrefix, t.agentName, turn)
}

// EscapeForLog escapes newlines and tabs for single-line log output.
func EscapeForLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// LogStreamDone logs stream completion stats
func LogStreamDone(provider string, duration time.Duration, chunks int) {
	if !enabled {
		return
	}
	logger.Info(fmt.Sprintf("[stream] %s done duration=%s chunks=%d", provider, duration.Round(time.Millisecond), chunks))
}

// LogTool logs tool execution with timing
func LogTool(name, id string, durationMs int64, success bool) {
	if !enabled {
		return
	}
	status := "ok"
	if !success {
		status = "error"
	}
	logger.Info(fmt.Sprintf("[tool] %s id=%s %dms %s", name, id, durationMs, status))
}
