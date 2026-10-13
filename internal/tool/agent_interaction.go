package tool

import (
	"context"
	"fmt"
	"strconv"

	"github.com/genai-io/san/internal/tool/perm"
)

// AgentInteractionSource identifies a worker interaction using executor-owned metadata.
type AgentInteractionSource struct {
	AgentID, TaskID, Name, ParentToolCallID, ToolCallID, Cwd, PermissionMode string
}

// Label renders identity as data, not terminal control sequences. Empty is main.
func (s AgentInteractionSource) Label() string {
	if s.AgentID == "" {
		return ""
	}
	id := s.TaskID
	if id == "" {
		id = s.AgentID
	}
	quoted := strconv.QuoteToGraphic(fmt.Sprintf("%s (%s)", s.Name, id))
	return quoted[1 : len(quoted)-1]
}

// AgentInteractions forwards worker interactions to the main session. Approvals
// apply only to the current invocation, never to persistent permission grants.
type AgentInteractions interface {
	Approve(ctx context.Context, source AgentInteractionSource, request *perm.PermissionRequest, input map[string]any) (bool, string)
	Ask(ctx context.Context, source AgentInteractionSource, request *QuestionRequest) (*QuestionResponse, error)
}
