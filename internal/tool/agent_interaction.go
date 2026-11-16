package tool

import (
	"context"
	"fmt"
	"strconv"

	"github.com/genai-io/san/internal/tool/perm"
)

// FlowOrigin attributes a worker to its flow without changing its routing address.
type FlowOrigin struct {
	// TaskID identifies the shared flow run, not a worker broker address.
	TaskID string `json:"task_id"`
	// Name is the flow definition's name.
	Name string `json:"name"`
	// NodeID identifies the node in the flow graph.
	NodeID string `json:"node_id"`
	// WorkerLabel identifies a for_each worker; empty for ordinary nodes.
	WorkerLabel string `json:"worker_label,omitempty"`
}

// AgentInteractionSource identifies a worker interaction using executor-owned metadata.
type AgentInteractionSource struct {
	AgentID          string
	TaskID           string
	AgentName        string
	ParentToolCallID string
	ToolCallID       string
	WorkingDirectory string
	PermissionMode   string
	Flow             *FlowOrigin
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
	label := fmt.Sprintf("%s (%s)", s.AgentName, id)
	if origin := s.Flow; origin != nil {
		label = fmt.Sprintf("flow %s / node %s", origin.Name, origin.NodeID)
		if origin.WorkerLabel != "" {
			label += " / worker " + origin.WorkerLabel
		}
		label += fmt.Sprintf(" (%s)", origin.TaskID)
	}
	quoted := strconv.QuoteToGraphic(label)
	return quoted[1 : len(quoted)-1]
}

// AgentInteractions forwards worker interactions to the main session. Approvals
// apply only to the current invocation, never to persistent permission grants.
type AgentInteractions interface {
	Approve(ctx context.Context, source AgentInteractionSource, request *perm.PermissionRequest, input map[string]any) (bool, string)
	Ask(ctx context.Context, source AgentInteractionSource, request *QuestionRequest) (*QuestionResponse, error)
}
