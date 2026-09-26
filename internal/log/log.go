package log

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// EscapeForLog escapes newlines and tabs for single-line log output.
func EscapeForLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}
