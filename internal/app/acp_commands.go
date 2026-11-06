package app

import (
	"fmt"
	"slices"

	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
)

func acpCommands(cwd string, plugins *plugin.Registry) *command.Registry {
	return command.NewRegistry(command.Options{CWD: cwd, PluginCommandPaths: func() []command.PluginCommandPath {
		var paths []command.PluginCommandPath
		for _, p := range plugins.CommandPaths() {
			paths = append(paths, command.PluginCommandPath{Path: p.Path, Namespace: p.Namespace, IsProject: p.Scope != plugin.ScopeUser})
		}
		return paths
	}})
}

// Only prompt commands are supported: no TUI settings or process-global actions.
func (s *acpSession) commandPrompt(text string) (string, string, error) {
	name, _, slash := command.ParseCommand(text)
	if !slash {
		return text, "", nil
	}
	c, ok := s.commands.IsCustomCommand(name)
	if !ok {
		return "", "", fmt.Errorf("unsupported ACP command /%s", name)
	}
	body := c.Instructions()
	if body == "" {
		return "", "", fmt.Errorf("command /%s has no instructions", name)
	}
	return "<command-name>" + c.FullName() + "</command-name>\n\n" + command.WrapInvocation(c.FullName(), body) + "\n\n" + text, s.runtimeBuildParams.Plugins.RootForPath(c.FilePath), nil
}

func (s *acpSession) sendCommands(turn *acp.Turn) error {
	catalog := s.commands.CustomCommands()
	if s.advertisedCommands != nil && slices.Equal(s.advertisedCommands, catalog) {
		return nil
	}
	var commands []protocol.AvailableCommand
	for _, c := range catalog {
		commands = append(commands, protocol.AvailableCommand{Name: c.Name, Description: c.Description})
	}
	if err := turn.SendUpdate(protocol.SessionNotification{Update: protocol.SessionUpdate{Value: protocol.SessionUpdateAvailableCommandsUpdateVariant{SessionUpdate: "available_commands_update", AvailableCommands: commands}}}); err != nil {
		return err
	}
	s.advertisedCommands = catalog
	return nil
}
