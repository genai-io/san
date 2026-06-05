package input

import (
	"context"
	"fmt"
	"strings"

	coremcp "github.com/genai-io/san/internal/mcp"
)

// HandleMCPCommand dispatches /mcp subcommands.
func HandleMCPCommand(ctx context.Context, selector *MCPSelector, width, height int, args string) (string, *coremcp.EditInfo, error) {
	if selector.registry == nil {
		return "MCP is not initialized.\n\nAdd MCP servers with:\n  /mcp add <name> -- <command> [args...]", nil, nil
	}

	args = strings.TrimSpace(args)
	parts := strings.Fields(args)

	if len(parts) == 0 {
		if err := selector.EnterSelect(width, height); err != nil {
			return "", nil, err
		}
		return "", nil, nil
	}

	subCmd := strings.ToLower(parts[0])
	var serverName string
	if len(parts) > 1 {
		serverName = parts[1]
	}

	switch subCmd {
	case "add":
		r, err := handleMCPAdd(selector.registry, ctx, parts[1:])
		return r, nil, err
	case "edit":
		return handleMCPEdit(selector.registry, serverName)
	case "remove":
		r, err := handleMCPRemove(selector.registry, serverName)
		return r, nil, err
	case "get":
		r, err := handleMCPGet(selector.registry, serverName)
		return r, nil, err
	case "connect":
		r, err := handleMCPConnect(selector.registry, ctx, serverName)
		return r, nil, err
	case "disconnect":
		r, err := handleMCPDisconnect(selector.registry, serverName)
		return r, nil, err
	case "reconnect":
		r, err := handleMCPReconnect(selector.registry, ctx, serverName)
		return r, nil, err
	case "list", "status":
		r, err := handleMCPList(selector.registry)
		return r, nil, err
	default:
		r, err := handleMCPConnect(selector.registry, ctx, subCmd)
		return r, nil, err
	}
}

func handleMCPList(reg *coremcp.Registry) (string, error) {
	servers := reg.List()

	if len(servers) == 0 {
		return "No MCP servers configured.\n\nAdd servers with:\n  /mcp add <name> -- <command> [args...]\n  /mcp add --transport http <name> <url>", nil
	}

	var sb strings.Builder
	sb.WriteString("MCP Servers:\n\n")

	for _, srv := range servers {
		icon, label := mcpStatusDisplay(srv.Status)
		scope := string(srv.Config.Scope)
		if scope == "" {
			scope = "local"
		}
		fmt.Fprintf(&sb, "  %s %s [%s] (%s, %s)\n", icon, srv.Config.Name, srv.Config.GetType(), scope, label)

		if srv.Status == coremcp.StatusConnected {
			if len(srv.Tools) > 0 {
				fmt.Fprintf(&sb, "    Tools: %d\n", len(srv.Tools))
			}
			if len(srv.Resources) > 0 {
				fmt.Fprintf(&sb, "    Resources: %d\n", len(srv.Resources))
			}
			if len(srv.Prompts) > 0 {
				fmt.Fprintf(&sb, "    Prompts: %d\n", len(srv.Prompts))
			}
		}

		if srv.Error != "" {
			fmt.Fprintf(&sb, "    Error: %s\n", srv.Error)
		}
	}

	sb.WriteString("\nCommands:\n")
	sb.WriteString("  /mcp add <name> ...     Add a server\n")
	sb.WriteString("  /mcp edit <name>        Edit server config in $EDITOR\n")
	sb.WriteString("  /mcp remove <name>      Remove a server\n")
	sb.WriteString("  /mcp get <name>         Show server details\n")
	sb.WriteString("  /mcp connect <name>     Connect to server\n")
	sb.WriteString("  /mcp disconnect <name>  Disconnect from server\n")
	sb.WriteString("  /mcp reconnect <name>   Reconnect to server\n")

	return sb.String(), nil
}

func handleMCPEdit(reg *coremcp.Registry, name string) (string, *coremcp.EditInfo, error) {
	if name == "" {
		return "Usage: /mcp edit <server-name>", nil, nil
	}
	info, err := coremcp.PrepareServerEdit(reg, name)
	if err != nil {
		return err.Error(), nil, nil
	}
	return "", info, nil
}

func handleMCPConnect(reg *coremcp.Registry, ctx context.Context, name string) (string, error) {
	if name == "" {
		return "Usage: /mcp connect <server-name>", nil
	}

	if _, ok := reg.GetConfig(name); !ok {
		return fmt.Sprintf("Server not found: %s\n\nUse /mcp list to see available servers.", name), nil
	}

	if err := reg.Connect(ctx, name); err != nil {
		return fmt.Sprintf("Failed to connect to %s: %v", name, err), nil
	}

	if client, ok := reg.GetClient(name); ok {
		tools := client.GetCachedTools()
		return fmt.Sprintf("Connected to %s\nTools available: %d", name, len(tools)), nil
	}

	return fmt.Sprintf("Connected to %s", name), nil
}

func handleMCPDisconnect(reg *coremcp.Registry, name string) (string, error) {
	if name == "" {
		return "Usage: /mcp disconnect <server-name>", nil
	}

	if err := reg.Disconnect(name); err != nil {
		return fmt.Sprintf("Failed to disconnect from %s: %v", name, err), nil
	}

	return fmt.Sprintf("Disconnected from %s", name), nil
}

func handleMCPAdd(reg *coremcp.Registry, ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return mcpAddUsage(), nil
	}

	var (
		transport  = "stdio"
		scope      = "local"
		envVars    []string
		headers    []string
		name       string
		positional []string
		dashIdx    = -1
	)

	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			dashIdx = i
			break
		}
		switch args[i] {
		case "--transport", "-t":
			if i+1 < len(args) {
				i++
				transport = args[i]
			}
		case "--scope", "-s":
			if i+1 < len(args) {
				i++
				scope = args[i]
			}
		case "--env", "-e":
			if i+1 < len(args) {
				i++
				envVars = append(envVars, args[i])
			}
		case "--header", "-H":
			if i+1 < len(args) {
				i++
				headers = append(headers, args[i])
			}
		default:
			positional = append(positional, args[i])
		}
	}

	if len(positional) == 0 {
		return mcpAddUsage(), nil
	}
	name = positional[0]

	var config coremcp.ServerConfig
	config.Type = coremcp.TransportType(transport)

	switch config.Type {
	case coremcp.TransportSTDIO:
		if dashIdx == -1 || dashIdx >= len(args)-1 {
			return "STDIO transport requires: /mcp add <name> -- <command> [args...]", nil
		}
		cmdArgs := args[dashIdx+1:]
		config.Command = cmdArgs[0]
		if len(cmdArgs) > 1 {
			config.Args = cmdArgs[1:]
		}

	case coremcp.TransportHTTP, coremcp.TransportSSE:
		if len(positional) < 2 {
			return fmt.Sprintf("%s transport requires a URL: /mcp add --transport %s <name> <url>", transport, transport), nil
		}
		config.URL = positional[1]
		config.Headers = coremcp.ParseKeyValues(headers, ":")

	default:
		return fmt.Sprintf("Unsupported transport type: %s (use stdio, http, or sse)", transport), nil
	}

	config.Env = coremcp.ParseKeyValues(envVars, "=")

	mcpScope := coremcp.ParseScope(scope)
	if err := reg.AddServer(name, config, mcpScope); err != nil {
		return fmt.Sprintf("Failed to add server: %v", err), nil
	}

	if err := reg.Connect(ctx, name); err != nil {
		return fmt.Sprintf("Added '%s' to %s scope, but failed to connect: %v", name, scope, err), nil
	}

	toolCount := 0
	if client, ok := reg.GetClient(name); ok {
		toolCount = len(client.GetCachedTools())
	}

	return fmt.Sprintf("Added and connected to '%s' (%s, %s scope)\nTools available: %d", name, transport, scope, toolCount), nil
}

func handleMCPRemove(reg *coremcp.Registry, name string) (string, error) {
	if name == "" {
		return "Usage: /mcp remove <server-name>", nil
	}

	if _, ok := reg.GetConfig(name); !ok {
		return fmt.Sprintf("Server not found: %s\n\nUse /mcp list to see available servers.", name), nil
	}

	if err := reg.RemoveServer(name); err != nil {
		return fmt.Sprintf("Failed to remove %s: %v", name, err), nil
	}

	return fmt.Sprintf("Removed server '%s'", name), nil
}

func handleMCPGet(reg *coremcp.Registry, name string) (string, error) {
	if name == "" {
		return "Usage: /mcp get <server-name>", nil
	}

	config, ok := reg.GetConfig(name)
	if !ok {
		return fmt.Sprintf("Server not found: %s\n\nUse /mcp list to see available servers.", name), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Server: %s\n", name)

	scope := string(config.Scope)
	if scope == "" {
		scope = "local"
	}
	fmt.Fprintf(&sb, "Scope:  %s\n", scope)
	fmt.Fprintf(&sb, "Type:   %s\n", config.GetType())

	switch config.GetType() {
	case coremcp.TransportSTDIO:
		cmd := config.Command
		if len(config.Args) > 0 {
			cmd += " " + strings.Join(config.Args, " ")
		}
		fmt.Fprintf(&sb, "Command: %s\n", cmd)
	case coremcp.TransportHTTP, coremcp.TransportSSE:
		fmt.Fprintf(&sb, "URL:    %s\n", config.URL)
	}

	if len(config.Env) > 0 {
		sb.WriteString("Env:\n")
		for k, v := range config.Env {
			masked := "***"
			if v == "" {
				masked = "(empty)"
			}
			fmt.Fprintf(&sb, "  %s=%s\n", k, masked)
		}
	}

	if len(config.Headers) > 0 {
		sb.WriteString("Headers:\n")
		for k, v := range config.Headers {
			masked := "***"
			if v == "" {
				masked = "(empty)"
			}
			fmt.Fprintf(&sb, "  %s: %s\n", k, masked)
		}
	}

	icon, label := mcpStatusDisplay(coremcp.StatusDisconnected)
	toolCount := 0
	if client, ok := reg.GetClient(name); ok {
		srv := client.ToServer()
		icon, label = mcpStatusDisplay(srv.Status)
		toolCount = len(srv.Tools)

		if srv.Error != "" {
			fmt.Fprintf(&sb, "Error:  %s\n", srv.Error)
		}
	}
	fmt.Fprintf(&sb, "Status: %s %s\n", icon, label)
	if toolCount > 0 {
		fmt.Fprintf(&sb, "Tools:  %d\n", toolCount)
	}

	return sb.String(), nil
}

func handleMCPReconnect(reg *coremcp.Registry, ctx context.Context, name string) (string, error) {
	if name == "" {
		return "Usage: /mcp reconnect <server-name>", nil
	}

	if _, ok := reg.GetConfig(name); !ok {
		return fmt.Sprintf("Server not found: %s\n\nUse /mcp list to see available servers.", name), nil
	}

	_ = reg.Disconnect(name)

	if err := reg.Connect(ctx, name); err != nil {
		return fmt.Sprintf("Failed to reconnect to %s: %v", name, err), nil
	}

	toolCount := 0
	if client, ok := reg.GetClient(name); ok {
		toolCount = len(client.GetCachedTools())
	}

	return fmt.Sprintf("Reconnected to %s\nTools available: %d", name, toolCount), nil
}

func mcpAddUsage() string {
	return `Usage: /mcp add [options] <name> [-- <command> [args...]] or <url>

Options:
  --transport <type>   Transport: stdio (default), http, sse
  --scope <scope>      Scope: local (default), project, user
  --env KEY=value      Environment variable (repeatable, STDIO only)
  --header Key:Value   HTTP header (repeatable, HTTP/SSE only)

Short flags: -t, -s, -e, -H

Examples:
  /mcp add myserver -- npx -y @modelcontextprotocol/server-filesystem .
  /mcp add --transport http pubmed https://pubmed.mcp.example.com/mcp
  /mcp add --transport http --scope project myapi https://api.example.com/mcp
  /mcp add --env API_KEY=xxx myserver -- npx -y some-mcp-server`
}
