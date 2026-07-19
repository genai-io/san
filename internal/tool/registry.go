package tool

import (
	"context"
	"strings"
	"sync"

	"github.com/genai-io/san/internal/tool/toolresult"
)

// Registry manages tool registration and execution
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a new tool registry
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
// Panics if the tool returns an empty name, which indicates a programming error.
func (r *Registry) Register(tool Tool) {
	if tool.Name() == "" {
		panic("tool: Register called with empty tool name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[strings.ToLower(tool.Name())] = tool
}

// RegisterAlias adds an additional name that resolves to the same tool.
// Use this for backward-compatible renames (e.g., AgentOutput → TaskOutput).
func (r *Registry) RegisterAlias(alias string, tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[strings.ToLower(alias)] = tool
}

// Unregister removes a tool by name. Returns true if the tool existed.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(name)
	_, ok := r.tools[key]
	if ok {
		delete(r.tools, key)
	}
	return ok
}

// Get retrieves a tool by name
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[strings.ToLower(name)]
	return tool, ok
}

// List returns all registered tool names
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Execute runs a tool by name with the given parameters
func (r *Registry) Execute(ctx context.Context, name string, params map[string]any, cwd string) toolresult.ToolResult {
	tool, ok := r.Get(name)
	if !ok {
		return toolresult.NewErrorResult(name, "unknown tool: "+name)
	}
	return tool.Execute(ctx, params, cwd)
}

// PopSideEffect retrieves and removes the side effect for a tool call.
func (r *Registry) PopSideEffect(toolCallID string) any { return PopSideEffect(toolCallID) }

// PopResultDetails retrieves structured result data for a tool call.
func (r *Registry) PopResultDetails(toolCallID string) any { return PopResultDetails(toolCallID) }

// defaultRegistry is the package-level tool registry, populated at init time.
var defaultRegistry = NewRegistry()

// Register adds a tool to the default registry.
// Called by tool subpackages during init().
func Register(tool Tool) {
	defaultRegistry.Register(tool)
}

// Unregister removes a tool from the default registry. Intended for test cleanup.
func Unregister(name string) {
	defaultRegistry.Unregister(name)
}

// Get retrieves a tool from the default registry.
func Get(name string) (Tool, bool) {
	return defaultRegistry.Get(name)
}

// List returns the names of every tool in the default registry (lower-cased,
// as stored; order unspecified).
func List() []string {
	return defaultRegistry.List()
}

// Execute runs a tool from the default registry.
func Execute(ctx context.Context, name string, params map[string]any, cwd string) toolresult.ToolResult {
	return defaultRegistry.Execute(ctx, name, params, cwd)
}
