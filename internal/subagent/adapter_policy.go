package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/genai-io/san/internal/tool"
)

// PrepareAgentTools returns both the actual catalogue for approval and the opaque
// policy the approved call must carry into execution.
func (a *ExecutorAdapter) PrepareAgentTools(req tool.AgentExecRequest) ([]string, any, error) {
	resolved, err := a.resolveToolPolicy(req)
	if err != nil {
		return nil, nil, err
	}
	return slices.Clone(resolved.tools), resolved, nil
}

// PrepareFlowWorker fingerprints authority, not descriptions, prompt text or the
// order in which MCP servers happen to advertise their tools.
func (a *ExecutorAdapter) PrepareFlowWorker(req tool.AgentExecRequest) (any, string, error) {
	resolved, err := a.resolveToolPolicy(req)
	if err != nil {
		return nil, "", err
	}
	names := slices.Clone(resolved.tools)
	slices.Sort(names)
	policy := struct {
		Mode        PermissionMode
		Tools       []string
		Allow, Deny ToolList
	}{resolved.mode, names, resolved.definition.AllowTools, resolved.definition.DenyTools}
	b, err := json.Marshal(policy)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(b)
	return resolved, hex.EncodeToString(sum[:]), nil
}
