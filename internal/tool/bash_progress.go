package tool

import "context"

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
