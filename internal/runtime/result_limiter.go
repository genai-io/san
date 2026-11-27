package runtime

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// ResultLimiter keeps oversized text out of the model's conversation. Its store
// and session identity are captured at construction, never read from a mutable
// current-session selector. A missing/failed store still produces a bounded
// preview, explicitly reporting that the full content was not persisted.
type ResultLimiter struct {
	Store     *session.Store
	SessionID string
}

func (p ResultLimiter) Filter(_ context.Context, c sdkagent.PostToolContext) (*sdkagent.Result, error) {
	const overflowThreshold = 100_000
	const previewSize = 10_000
	full := c.Result.Content.Text()
	if len(full) <= overflowThreshold {
		return nil, nil
	}
	preview := core.UTF8Prefix(full, previewSize)
	out := c.Result
	if p.Store != nil && p.SessionID != "" {
		if err := p.Store.PersistToolResult(p.SessionID, c.Call.ID, full); err == nil {
			out.Content = ai.TextContent(fmt.Sprintf("%s\n\n[Full output persisted to blobs/tool-result/%s/%s]", preview, p.SessionID, c.Call.ID))
			return &out, nil
		}
	}
	out.Content = ai.TextContent(fmt.Sprintf("%s\n\n[Output truncated from %d bytes — full content not persisted]", preview, len(full)))
	return &out, nil
}
