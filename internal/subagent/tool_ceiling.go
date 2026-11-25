package subagent

import (
	"fmt"
	"slices"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
)

// SetParentTools installs the live parent catalogue getter before launching workers.
// Without a getter only worker policy applies. An installed getter returning no names grants none.
func (e *Executor) SetParentTools(get func() []string) { e.parentTools = get }

func (e *Executor) parentCeiling() []string {
	if e.parentTools == nil {
		return nil
	}
	names := e.parentTools()
	if names == nil {
		return []string{}
	}
	return names
}

func within(names []string, name string) bool { return names == nil || slices.Contains(names, name) }

// workerSchemas is shared by preparation and every attempt (initial, fallback or continuation).
func (e *Executor) workerSchemas(config *AgentConfig, mode PermissionMode, ceiling []string) []core.ToolSchema {
	var mcp func() []core.ToolSchema
	if e.mcpManager != nil {
		mcp = e.mcpManager.ToolSchemas
	}
	set := newAgentToolSet(config.AllowTools.Names(), config.DenyTools.BareNames(), e.disabledToolsSnapshot(), mcp)
	schemas := filterSchemasForPermission(set.Tools(), mode, config.AllowTools, e.interactions != nil || e.permissionResolver != nil)
	parent := e.parentCeiling()
	return slices.DeleteFunc(schemas, func(s core.ToolSchema) bool {
		return tool.IsParentOnlyTool(s.Name) || !within(ceiling, s.Name) || !within(parent, s.Name)
	})
}

// WorkerPolicy pins a worker's effective authority before approval or scheduling.
// Flow persists only a fingerprint; the definition stays in memory.
type WorkerPolicy struct {
	Mode        PermissionMode `json:"mode"`
	Definition  *AgentConfig   `json:"definition"`
	Tools       []string       `json:"tools"`
	DisplayOnly bool           `json:"display_only,omitempty"`
}

// PinWorkerPolicy resolves and validates the request without launching a worker.
func (e *Executor) PinWorkerPolicy(req tool.AgentExecRequest) (*WorkerPolicy, error) {
	var original []string
	var config *AgentConfig
	var label bool
	if pin, ok := req.ResolvedAgentConfig.(*WorkerPolicy); ok {
		if pin == nil || pin.Definition == nil || pin.Tools == nil {
			return nil, fmt.Errorf("incomplete pinned worker policy")
		}
		config, original, label = pin.Definition, pin.Tools, pin.DisplayOnly
	} else {
		var ok bool
		config, ok = e.resolveRequestAgentConfig(req)
		if !ok {
			return nil, fmt.Errorf("unknown or disabled agent: %s", req.Agent)
		}
		label = config.displayOnly
	}
	config = &AgentConfig{Name: config.Name, Description: config.Description, Color: config.Color, WhenToUse: config.WhenToUse, Model: config.Model, PermissionMode: config.PermissionMode, AllowTools: slices.Clone(config.AllowTools), DenyTools: slices.Clone(config.DenyTools), Skills: slices.Clone(config.Skills), SystemPrompt: config.GetSystemPrompt(), MaxSteps: config.MaxSteps, Source: config.Source, McpServers: slices.Clone(config.McpServers), displayOnly: label}
	mode := e.requestPermissionMode(config, req)
	if pin, ok := req.ResolvedAgentConfig.(*WorkerPolicy); ok && pin.Mode != mode {
		return nil, fmt.Errorf("worker permission mode changed after policy was pinned")
	}
	schemas := e.workerSchemas(config, mode, original)
	names := make([]string, 0, len(schemas))
	for _, s := range schemas {
		if within(req.Tools, s.Name) {
			names = append(names, s.Name)
		}
	}
	for i, name := range req.Tools {
		if !slices.Contains(names, name) {
			return nil, fmt.Errorf("tool %q is not available to this worker", name)
		}
		if slices.Contains(req.Tools[:i], name) {
			return nil, fmt.Errorf("duplicate worker tool %q", name)
		}
	}
	return &WorkerPolicy{Mode: mode, Definition: config, Tools: names, DisplayOnly: label}, nil
}
