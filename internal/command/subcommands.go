package command

import "strings"

// builtinSubcommands keeps the discoverable command paths together. Names are
// complete paths so a suggestion can be inserted directly into the input.
// Commands with state-dependent arguments (group and workflow names) add those
// suggestions in the app, after this static catalog has chosen the subcommand.
var builtinSubcommands = map[string][]Info{
	"workflow": {
		{Name: "workflow list", Description: "List saved workflows"},
		{Name: "workflow show", Description: "Preview a saved workflow graph"},
		{Name: "workflow run", Description: "Run a saved workflow with optional key=value inputs"},
		{Name: "workflow stop", Description: "Stop a running workflow"},
	},
	"loop": {
		{Name: "loop once", Description: "Schedule a prompt to run once"},
		{Name: "loop list", Description: "List scheduled jobs"},
		{Name: "loop delete", Description: "Cancel a scheduled job"},
	},
	"memory": {
		{Name: "memory list", Description: "List memory files"},
		{Name: "memory show", Description: "Show memory files"},
		{Name: "memory edit", Description: "Edit a memory file"},
	},
	"memory edit": {
		{Name: "memory edit global", Description: "Edit user memory"},
		{Name: "memory edit project", Description: "Edit project memory"},
		{Name: "memory edit local", Description: "Edit local memory"},
	},
	"mcp": {
		{Name: "mcp list", Description: "List MCP servers"},
		{Name: "mcp add", Description: "Add an MCP server"},
		{Name: "mcp get", Description: "Show MCP server details"},
		{Name: "mcp edit", Description: "Edit an MCP server"},
		{Name: "mcp remove", Description: "Remove an MCP server"},
		{Name: "mcp connect", Description: "Connect an MCP server"},
		{Name: "mcp disconnect", Description: "Disconnect an MCP server"},
		{Name: "mcp reconnect", Description: "Reconnect an MCP server"},
	},
	"plugin": {
		{Name: "plugin list", Description: "List installed plugins"},
		{Name: "plugin install", Description: "Install a plugin"},
		{Name: "plugin marketplace", Description: "Manage plugin marketplaces"},
		{Name: "plugin enable", Description: "Enable a plugin"},
		{Name: "plugin disable", Description: "Disable a plugin"},
		{Name: "plugin info", Description: "Show plugin details"},
		{Name: "plugin errors", Description: "Show plugin errors"},
	},
	"plugin marketplace": {
		{Name: "plugin marketplace list", Description: "List marketplaces"},
		{Name: "plugin marketplace add", Description: "Add a marketplace"},
		{Name: "plugin marketplace remove", Description: "Remove a marketplace"},
		{Name: "plugin marketplace sync", Description: "Sync a marketplace"},
	},
	"context": {
		{Name: "context limit", Description: "Override the context and output limits"},
	},
	"context limit": {
		{Name: "context limit reset", Description: "Clear the context limit override"},
	},
	"init": {
		{Name: "init local", Description: "Create local instructions"},
		{Name: "init rules", Description: "Create the rules directory"},
	},
}

// matchingSubcommands completes one path segment at a time. A trailing space
// enters the next level; a completed leaf has no further static suggestions.
func matchingSubcommands(query string) []Info {
	at := strings.LastIndex(query, " ")
	if at < 0 {
		return nil
	}
	parent, fragment := query[:at], query[at+1:]
	var matches []Info
	for _, candidate := range builtinSubcommands[parent] {
		leaf := strings.TrimPrefix(candidate.Name, parent+" ")
		if strings.HasPrefix(leaf, fragment) {
			matches = append(matches, candidate)
		}
	}
	return matches
}
