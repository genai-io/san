package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/genai-io/san/v2/internal/plugin"
	"github.com/spf13/cobra"
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage plugins",
	Long: `Manage plugins for extending San with skills, agents, hooks, and MCP servers.

Plugins bundle multiple components:
  - Skills: Custom skills invokable via slash commands
  - Agents: Subagent definitions
  - Hooks: Event handlers
  - MCP Servers: Model Context Protocol servers
  - LSP Servers: Language Server Protocol servers

Configuration files:
  ~/.san/plugins/                  User-level plugins
  ./.san/plugins/                  Project-level plugins
  ./.san/plugins-local/            Local plugins (git-ignored)
  ~/.san/settings.json             Enabled plugins (user)
  ./.san/settings.json             Enabled plugins (project)`,
}

var pluginScope string

func init() {
	// Add subcommands
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginInstallCmd)
	pluginCmd.AddCommand(pluginUninstallCmd)
	pluginCmd.AddCommand(pluginEnableCmd)
	pluginCmd.AddCommand(pluginDisableCmd)
	pluginCmd.AddCommand(pluginValidateCmd)
	pluginCmd.AddCommand(pluginInfoCmd)

	// Add flags
	pluginInstallCmd.Flags().StringVarP(&pluginScope, "scope", "s", "user", "Install scope (user, project, local)")
	pluginUninstallCmd.Flags().StringVarP(&pluginScope, "scope", "s", "user", "Uninstall scope (user, project, local)")
	pluginEnableCmd.Flags().StringVarP(&pluginScope, "scope", "s", "user", "Settings scope (user, project, local)")
	pluginDisableCmd.Flags().StringVarP(&pluginScope, "scope", "s", "user", "Settings scope (user, project, local)")
}

// loadPlugins loads plugins from standard directories and the optional --plugin-dir flag.
func loadPlugins(ctx context.Context, cwd string) error {
	if err := plugin.Default().Load(ctx, cwd); err != nil {
		return fmt.Errorf("failed to load plugins: %w", err)
	}
	_ = plugin.Default().LoadClaudePlugins(ctx)
	if cliOpts.pluginDir != "" {
		if err := plugin.Default().LoadFromPath(ctx, cliOpts.pluginDir); err != nil {
			return fmt.Errorf("failed to load plugin from %s: %w", cliOpts.pluginDir, err)
		}
	}
	return nil
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed plugins",
	Long:  "List all installed plugins with their status.",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cwd, _ := os.Getwd()

		if err := loadPlugins(ctx, cwd); err != nil {
			return err
		}

		plugins := plugin.Default().List()
		if len(plugins) == 0 {
			fmt.Println("No plugins installed.")
			fmt.Println("\nInstall a plugin with:")
			fmt.Println("  san plugin install <plugin>@<marketplace>")
			return nil
		}

		fmt.Printf("Plugins (%d installed, %d enabled):\n\n",
			plugin.Default().Count(),
			plugin.Default().EnabledCount())

		for _, p := range plugins {
			printPluginSummary(p)
		}

		fmt.Println("\nLegend: ● enabled  ○ disabled  👤 user  📁 project  💻 local")
		return nil
	},
}

// printPluginSummary prints a single plugin's summary to stdout.
func printPluginSummary(p *plugin.Plugin) {
	status := "○"
	if p.Enabled {
		status = "●"
	}

	fmt.Printf("  %s %s %s (%s)\n", status, p.Scope.Icon(), p.FullName(), p.Scope)

	if p.Manifest.Description != "" {
		fmt.Printf("      %s\n", p.Manifest.Description)
	}

	components := formatPluginComponents(p)
	if len(components) > 0 {
		fmt.Printf("      Components: %s\n", strings.Join(components, ", "))
	}
}

// formatPluginComponents returns a list of component count strings.
func formatPluginComponents(p *plugin.Plugin) []string {
	var components []string
	if n := len(p.Components.Skills); n > 0 {
		components = append(components, fmt.Sprintf("%d skills", n))
	}
	if n := len(p.Components.Agents); n > 0 {
		components = append(components, fmt.Sprintf("%d agents", n))
	}
	if n := len(p.Components.Commands); n > 0 {
		components = append(components, fmt.Sprintf("%d commands", n))
	}
	if p.Components.Hooks != nil {
		if n := len(p.Components.Hooks.Hooks); n > 0 {
			components = append(components, fmt.Sprintf("%d hooks", n))
		}
	}
	if n := len(p.Components.MCP); n > 0 {
		components = append(components, fmt.Sprintf("%d MCP", n))
	}
	if n := len(p.Components.LSP); n > 0 {
		components = append(components, fmt.Sprintf("%d LSP", n))
	}
	return components
}

var pluginInstallCmd = &cobra.Command{
	Use:   "install <plugin>@<marketplace>",
	Short: "Install a plugin",
	Long: `Install a plugin from a marketplace.

Examples:
  san plugin install git@my-plugins
  san plugin install deployment-tools@enterprise-plugins --scope project`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cwd, _ := os.Getwd()
		ref := args[0]

		if err := loadPlugins(ctx, cwd); err != nil {
			return err
		}

		installer := plugin.NewInstaller(plugin.Default(), cwd)
		_ = installer.LoadMarketplaces() // Non-fatal, continue with empty marketplaces

		scope := parsePluginScope(pluginScope)
		if err := installer.Install(ctx, ref, scope); err != nil {
			return fmt.Errorf("failed to install plugin: %w", err)
		}

		fmt.Printf("Installed plugin '%s' to %s scope\n", ref, pluginScope)
		return nil
	},
}

var pluginUninstallCmd = &cobra.Command{
	Use:   "uninstall <plugin>",
	Short: "Uninstall a plugin",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cwd, _ := os.Getwd()
		name := args[0]

		if err := loadPlugins(ctx, cwd); err != nil {
			return err
		}

		installer := plugin.NewInstaller(plugin.Default(), cwd)
		scope := parsePluginScope(pluginScope)

		if err := installer.Uninstall(name, scope); err != nil {
			return fmt.Errorf("failed to uninstall plugin: %w", err)
		}

		fmt.Printf("Uninstalled plugin '%s'\n", name)
		return nil
	},
}

var pluginEnableCmd = &cobra.Command{
	Use:   "enable <plugin>",
	Short: "Enable a plugin",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cwd, _ := os.Getwd()
		name := args[0]

		if err := loadPlugins(ctx, cwd); err != nil {
			return err
		}

		scope := parsePluginScope(pluginScope)
		if err := plugin.Default().Enable(name, scope); err != nil {
			return fmt.Errorf("failed to enable plugin: %w", err)
		}

		fmt.Printf("Enabled plugin '%s'\n", name)
		return nil
	},
}

var pluginDisableCmd = &cobra.Command{
	Use:   "disable <plugin>",
	Short: "Disable a plugin",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cwd, _ := os.Getwd()
		name := args[0]

		if err := loadPlugins(ctx, cwd); err != nil {
			return err
		}

		scope := parsePluginScope(pluginScope)
		if err := plugin.Default().Disable(name, scope); err != nil {
			return fmt.Errorf("failed to disable plugin: %w", err)
		}

		fmt.Printf("Disabled plugin '%s'\n", name)
		return nil
	},
}

var pluginValidateCmd = &cobra.Command{
	Use:   "validate [path]",
	Short: "Validate a plugin directory",
	Long:  "Validate a plugin directory structure and manifest.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}

		if err := plugin.ValidatePlugin(path); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}

		fmt.Println("Plugin validation passed!")
		return nil
	},
}

var pluginInfoCmd = &cobra.Command{
	Use:   "info <plugin>",
	Short: "Show plugin details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cwd, _ := os.Getwd()
		name := args[0]

		if err := loadPlugins(ctx, cwd); err != nil {
			return err
		}

		p, ok := plugin.Default().Get(name)
		if !ok {
			return fmt.Errorf("plugin not found: %s", name)
		}

		fmt.Printf("Plugin: %s\n", p.FullName())
		fmt.Printf("Scope: %s\n", p.Scope)
		fmt.Printf("Enabled: %v\n", p.Enabled)
		fmt.Printf("Path: %s\n", p.Path)

		if p.Manifest.Version != "" {
			fmt.Printf("Version: %s\n", p.Manifest.Version)
		}
		if p.Manifest.Description != "" {
			fmt.Printf("Description: %s\n", p.Manifest.Description)
		}
		if p.Manifest.Author != nil && p.Manifest.Author.Name != "" {
			fmt.Printf("Author: %s\n", p.Manifest.Author.Name)
		}
		if p.Manifest.Repository != "" {
			fmt.Printf("Repository: %s\n", p.Manifest.Repository)
		}
		if p.Manifest.License != "" {
			fmt.Printf("License: %s\n", p.Manifest.License)
		}

		fmt.Println("\nComponents:")
		if len(p.Components.Commands) > 0 {
			fmt.Printf("  Commands (%d):\n", len(p.Components.Commands))
			for _, c := range p.Components.Commands {
				fmt.Printf("    - %s\n", c)
			}
		}
		if len(p.Components.Skills) > 0 {
			fmt.Printf("  Skills (%d):\n", len(p.Components.Skills))
			for _, s := range p.Components.Skills {
				fmt.Printf("    - %s\n", s)
			}
		}
		if len(p.Components.Agents) > 0 {
			fmt.Printf("  Agents (%d):\n", len(p.Components.Agents))
			for _, a := range p.Components.Agents {
				fmt.Printf("    - %s\n", a)
			}
		}
		if p.Components.Hooks != nil && len(p.Components.Hooks.Hooks) > 0 {
			fmt.Printf("  Hooks (%d events):\n", len(p.Components.Hooks.Hooks))
			for event := range p.Components.Hooks.Hooks {
				fmt.Printf("    - %s\n", event)
			}
		}
		if len(p.Components.MCP) > 0 {
			fmt.Printf("  MCP Servers (%d):\n", len(p.Components.MCP))
			for name := range p.Components.MCP {
				fmt.Printf("    - %s\n", name)
			}
		}
		if len(p.Components.LSP) > 0 {
			fmt.Printf("  LSP Servers (%d):\n", len(p.Components.LSP))
			for name := range p.Components.LSP {
				fmt.Printf("    - %s\n", name)
			}
		}

		return nil
	},
}

func parsePluginScope(s string) plugin.Scope {
	switch strings.ToLower(s) {
	case "user", "global":
		return plugin.ScopeUser
	case "project":
		return plugin.ScopeProject
	case "local":
		return plugin.ScopeLocal
	default:
		return plugin.ScopeUser
	}
}

func init() {
	rootCmd.AddCommand(pluginCmd)
}
