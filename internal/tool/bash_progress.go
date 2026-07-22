package tool

import (
	"context"
	"fmt"
)

// BashProgress reports the cumulative output a running foreground command has
// produced so far — total lines and bytes across stdout and stderr — so the UI
// can show a live counter and a long-running command reads as making progress
// rather than being stuck. It is called repeatedly, already throttled by the
// caller; the same call may report the same or a growing count.
type BashProgress func(lines int, bytes int64)

type bashProgressKey struct{}

// ContextWithBashProgress stores a per-execution output-progress reporter in ctx.
// A nil reporter leaves ctx untouched, so the command runs without reporting.
func ContextWithBashProgress(ctx context.Context, report BashProgress) context.Context {
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, bashProgressKey{}, report)
}

// BashProgressFromContext resolves the current output-progress reporter, or nil
// when no consumer is listening.
func BashProgressFromContext(ctx context.Context) BashProgress {
	if ctx == nil {
		return nil
	}
	report, _ := ctx.Value(bashProgressKey{}).(BashProgress)
	return report
}

// formatBashProgress renders a running command's cumulative output as a short
// counter for its live row: line count once any newline has arrived, falling
// back to a byte size for output that has not broken a line yet (a progress
// bar, a single long token). Empty output yields "" so nothing is shown.
func formatBashProgress(lines int, bytes int64) string {
	switch {
	case lines == 1:
		return "1 line"
	case lines >= 1000:
		return fmt.Sprintf("%.1fk lines", float64(lines)/1000)
	case lines > 1:
		return fmt.Sprintf("%d lines", lines)
	case bytes <= 0:
		return ""
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
}
