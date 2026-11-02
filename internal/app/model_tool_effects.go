// Large tool output persistence. Workspace result effects live in agent.PostTool.
package app

import (
	"context"
	"fmt"
	"unicode/utf8"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// filterOversizedResult keeps a result too large for the window out of the conversation:
// the full text goes to the session store and the model is told a preview plus
// where the rest is.
//
// It is core.ResultFilter — the loop's PostTool — because that is the only seam that
// changes what the model is told. It used to edit the copy the interface draws
// instead, which was the same object back when the chat rows were the
// conversation; once the agent held its own, the guard went on protecting the
// screen from an output that was still reaching the model in full.
func (m *model) filterOversizedResult(_ context.Context, c sdkagent.PostToolContext) (*sdkagent.Result, error) {
	const overflowThreshold = 100_000
	const previewSize = 10_000

	// Paging applies to what a tool wrote, which is text. A picture a tool
	// returned is bounded by what the protocol accepts and is not what makes a
	// result outgrow the window.
	full := c.Result.Content.Text()
	if len(full) <= overflowThreshold {
		return nil, nil
	}
	cutoff := min(previewSize, len(full))
	for cutoff > 0 && !utf8.RuneStart(full[cutoff]) {
		cutoff--
	}
	preview := full[:cutoff]

	// A panic here is not recovered — the SDK says so of every hook — and a
	// session that is not up yet is an ordinary state, not a failure. Without
	// one the model still gets the preview; it just cannot be pointed at the
	// rest.
	out := c.Result
	if m.ensureSessionStore() == nil && m.sessionID() != "" {
		if err := m.services.SessionStore.PersistToolResult(m.sessionID(), c.Call.ID, full); err == nil {
			out.Content = ai.TextContent(fmt.Sprintf("%s\n\n[Full output persisted to blobs/tool-result/%s/%s]",
				preview, m.sessionID(), c.Call.ID))
			return &out, nil
		}
	}
	out.Content = ai.TextContent(fmt.Sprintf("%s\n\n[Output truncated from %d bytes — full content not persisted]",
		preview, len(full)))
	return &out, nil
}
