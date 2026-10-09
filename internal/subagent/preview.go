package subagent

// BuiltinToolNames previews the built-in tool schemas visible to an unnamed
// agent in mode. A run may also receive MCP tools, and disabled-tool settings
// can narrow this list. Bash is shown when available, but only read-only
// commands pass the permission gate in explore and edit modes.
func BuiltinToolNames(mode PermissionMode) []string {
	set := newAgentToolSet(nil, nil, nil, nil)
	schemas := filterSchemasForPermission(set.Tools(), mode, nil)
	names := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		names = append(names, schema.Name)
	}
	return names
}
