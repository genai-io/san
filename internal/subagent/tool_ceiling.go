package subagent

import (
	"fmt"
	"regexp"
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

// workerPolicy stays in memory; Flow persists only its fingerprint.
type workerPolicy struct {
	mode       PermissionMode
	definition *AgentConfig
	tools      []string
}

var exactWorkerTool = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (e *Executor) pinWorkerPolicy(req tool.AgentExecRequest) (*workerPolicy, error) {
	var original []string
	var config *AgentConfig
	pin, pinned := req.ResolvedAgentConfig.(*workerPolicy)
	if pinned {
		if pin == nil || pin.definition == nil || pin.tools == nil {
			return nil, fmt.Errorf("incomplete pinned worker policy")
		}
		config, original = pin.definition, pin.tools
	} else {
		var ok bool
		config, ok = e.resolveRequestAgentConfig(req)
		if !ok {
			return nil, fmt.Errorf("unknown or disabled agent: %s", req.Agent)
		}
		config = config.pinnedDefinition()
	}
	mode := e.requestPermissionMode(config, req)
	if pinned && pin.mode != mode {
		return nil, fmt.Errorf("worker permission mode changed after policy was pinned")
	}
	schemas := e.workerSchemas(config, mode, original)
	// A pinned policy already validated the requested names. Re-intersect at
	// launch: a denied or subsequently revoked tool must stay absent, not turn
	// a valid worker into an invalid request.
	names := make([]string, 0, len(schemas))
	for _, s := range schemas {
		if within(req.AllowTools, s.Name) {
			names = append(names, s.Name)
		}
	}
	for i, name := range req.AllowTools {
		if !pinned && !slices.Contains(names, name) {
			return nil, fmt.Errorf("tool %q is not available to this worker", name)
		}
		if slices.Contains(req.AllowTools[:i], name) {
			return nil, fmt.Errorf("duplicate worker tool %q", name)
		}
	}
	denied := make(map[string]bool, len(req.DenyTools))
	for _, name := range req.DenyTools {
		if !exactWorkerTool.MatchString(name) {
			return nil, fmt.Errorf("invalid denied worker tool %q", name)
		}
		if denied[name] {
			return nil, fmt.Errorf("duplicate denied worker tool %q", name)
		}
		denied[name] = true
	}
	names = slices.DeleteFunc(names, func(name string) bool { return denied[name] })
	return &workerPolicy{mode: mode, definition: config, tools: names}, nil
}

// Materialize the lazy prompt without copying its sync.Once or retaining its source file.
func (c *AgentConfig) pinnedDefinition() *AgentConfig {
	return &AgentConfig{
		Name: c.Name, Description: c.Description, Color: c.Color, WhenToUse: c.WhenToUse,
		Model: c.Model, PermissionMode: c.PermissionMode, MaxSteps: c.MaxSteps,
		AllowTools: slices.Clone(c.AllowTools), DenyTools: slices.Clone(c.DenyTools),
		Skills: slices.Clone(c.Skills), McpServers: slices.Clone(c.McpServers),
		SystemPrompt: c.GetSystemPrompt(), Source: c.Source, displayOnly: c.displayOnly,
	}
}
