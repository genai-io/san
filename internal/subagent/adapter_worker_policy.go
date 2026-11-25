package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/genai-io/san/internal/tool"
)

// PinAgentTools returns both the actual catalogue for approval and the opaque
// policy the approved call must carry into execution.
func (a *ExecutorAdapter) PinAgentTools(req tool.AgentExecRequest) ([]string, any, error) {
	pin, err := a.PinWorkerPolicy(req)
	if err != nil {
		return nil, nil, err
	}
	return slices.Clone(pin.Tools), pin, nil
}

// PinFlowWorker fingerprints authority, not descriptions, prompt text or the
// order in which MCP servers happen to advertise their tools.
func (a *ExecutorAdapter) PinFlowWorker(req tool.AgentExecRequest) (any, string, error) {
	pin, err := a.PinWorkerPolicy(req)
	if err != nil {
		return nil, "", err
	}
	names := slices.Clone(pin.Tools)
	slices.Sort(names)
	policy := struct {
		Mode        PermissionMode
		Tools       []string
		Allow, Deny ToolList
	}{pin.Mode, names, pin.Definition.AllowTools, pin.Definition.DenyTools}
	b, err := json.Marshal(policy)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(b)
	return pin, hex.EncodeToString(sum[:]), nil
}
