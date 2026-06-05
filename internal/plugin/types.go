// Package plugin provides plugin management for San.
// Plugins bundle skills, agents, hooks, MCP servers, and LSP servers into
// installable and shareable units. Compatible with Claude Code plugin format.
package plugin

import "github.com/genai-io/san/v2/internal/setting"

// Scope represents where a plugin is installed.
type Scope string

const (
	// ScopeUser is ~/.san/plugins/ (personal plugins, default)
	ScopeUser Scope = "user"

	// ScopeProject is .san/plugins/ (team plugins, git-tracked)
	ScopeProject Scope = "project"

	// ScopeLocal is .san/plugins-local/ (local plugins, gitignored)
	ScopeLocal Scope = "local"

	// ScopeManaged is managed plugins (read-only, system-level)
	ScopeManaged Scope = "managed"
)

// String returns the display name for the scope.
func (s Scope) String() string {
	return string(s)
}

// Icon returns a display icon for the scope.
func (s Scope) Icon() string {
	switch s {
	case ScopeUser:
		return "👤"
	case ScopeProject:
		return "📁"
	case ScopeLocal:
		return "💻"
	case ScopeManaged:
		return "🔒"
	default:
		return "?"
	}
}

// Plugin represents a loaded plugin with all its components.
type Plugin struct {
	// Manifest contains plugin metadata
	Manifest Manifest

	// Path is the absolute path to the plugin root directory
	Path string

	// Scope indicates where this plugin is installed
	Scope Scope

	// Enabled indicates if this plugin is currently enabled
	Enabled bool

	// Source is the original installation source (e.g., "git@marketplace")
	Source string

	// Components contains resolved paths to plugin components
	Components Components

	// Errors contains any errors encountered while loading components
	Errors []string
}

// Name returns the plugin name, preferring manifest name over directory name.
func (p *Plugin) Name() string {
	if p.Manifest.Name != "" {
		return p.Manifest.Name
	}
	return ""
}

// FullName returns the plugin name with marketplace suffix (e.g., "git@my-plugins").
func (p *Plugin) FullName() string {
	if p.Source != "" {
		return p.Source
	}
	return p.Name()
}

// Components contains resolved paths to plugin components.
type Components struct {
	// Commands are paths to command markdown files
	Commands []string

	// Skills are paths to skill directories (containing SKILL.md)
	Skills []string

	// Agents are paths to agent markdown files
	Agents []string

	// Hooks is the parsed hooks configuration
	Hooks *HooksConfig

	// MCP contains MCP server configurations
	MCP map[string]MCPServerConfig

	// LSP contains LSP server configurations
	LSP map[string]LSPServerConfig
}

// HooksConfig represents the hooks configuration from a plugin.
type HooksConfig struct {
	Hooks map[string][]HookMatcher `json:"hooks"`
}

// HookMatcher represents a hook matcher with associated hook commands.
type HookMatcher struct {
	Matcher string            `json:"matcher,omitempty"`
	Hooks   []setting.HookCmd `json:"hooks"`
}

// MCPServerConfig represents an MCP server configuration from a plugin.
type MCPServerConfig struct {
	// Type is the transport type (stdio, http, sse)
	Type string `json:"type,omitempty"`

	// STDIO transport
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// HTTP/SSE transport
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// LSPServerConfig represents an LSP server configuration from a plugin.
type LSPServerConfig struct {
	Command             string            `json:"command"`
	Args                []string          `json:"args,omitempty"`
	ExtensionToLanguage map[string]string `json:"extensionToLanguage,omitempty"`
}

// Manifest represents plugin metadata from plugin.json.
// Compatible with Claude Code .claude-plugin/plugin.json format.
type Manifest struct {
	// Required field
	Name string `json:"name"`

	// Metadata fields
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      *Author  `json:"author,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	License     string   `json:"license,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`

	// Component path fields - can be string, []string, or inline object
	// Use any type and resolve during loading
	Commands   any `json:"commands,omitempty"`
	Agents     any `json:"agents,omitempty"`
	Skills     any `json:"skills,omitempty"`
	Hooks      any `json:"hooks,omitempty"`
	MCPServers any `json:"mcpServers,omitempty"`
	LSPServers any `json:"lspServers,omitempty"`
}

// Author represents plugin author information.
type Author struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// AuthorFromAny parses author from either a string or Author object.
func AuthorFromAny(v any) *Author {
	if v == nil {
		return nil
	}
	switch a := v.(type) {
	case string:
		return &Author{Name: a}
	case map[string]any:
		author := &Author{}
		if name, ok := a["name"].(string); ok {
			author.Name = name
		}
		if email, ok := a["email"].(string); ok {
			author.Email = email
		}
		if url, ok := a["url"].(string); ok {
			author.URL = url
		}
		return author
	default:
		return nil
	}
}

// InstalledPluginsV2 represents the installed_plugins.json version 2 format.
// This is compatible with Claude Code's format.
type InstalledPluginsV2 struct {
	Version int                            `json:"version"` // Always 2
	Plugins map[string][]PluginInstallInfo `json:"plugins"` // key: "name@marketplace"
}

// PluginInstallInfo represents a single installation of a plugin.
// Multiple installations can exist for the same plugin (different scopes/versions).
type PluginInstallInfo struct {
	Scope        string `json:"scope"`                  // "user", "project"
	InstallPath  string `json:"installPath"`            // Full path to plugin
	Version      string `json:"version,omitempty"`      // Semver or commit SHA
	InstalledAt  string `json:"installedAt"`            // ISO 8601 timestamp
	LastUpdated  string `json:"lastUpdated,omitempty"`  // ISO 8601 timestamp
	GitCommitSha string `json:"gitCommitSha,omitempty"` // For git-based installs
}

// InstalledPlugin is a simplified representation for backward compatibility.
type InstalledPlugin struct {
	Name        string `json:"name"`
	Source      string `json:"source"`      // e.g., "git@my-marketplace"
	Path        string `json:"path"`        // Install path
	Version     string `json:"version"`     // Installed version
	InstalledAt string `json:"installedAt"` // ISO timestamp
}

// PluginState represents the enabled/disabled state for plugins.
type PluginState struct {
	// Plugins maps plugin full name to enabled state
	Plugins map[string]bool `json:"plugins"`
}

// KnownMarketplacesV2 represents the known_marketplaces.json format.
// Compatible with Claude Code's format.
type KnownMarketplacesV2 struct {
	// Key is marketplace ID (e.g., "cc-plugins", "claude-plugins-official")
	Marketplaces map[string]MarketplaceEntry `json:"-"`
}

// MarketplaceEntry represents a marketplace configuration.
type MarketplaceEntry struct {
	Source          MarketplaceSourceInfo `json:"source"`
	InstallLocation string                `json:"installLocation,omitempty"`
	LastUpdated     string                `json:"lastUpdated,omitempty"`
}

// MarketplaceSourceInfo represents the source of a marketplace.
type MarketplaceSourceInfo struct {
	Source string `json:"source"` // "github" or "directory"
	Repo   string `json:"repo,omitempty"`
	Path   string `json:"path,omitempty"`
}

// MarketplaceSource represents a plugin marketplace (legacy format).
type MarketplaceSource struct {
	Name        string `json:"name"`
	Type        string `json:"type"`                 // "directory", "github"
	Path        string `json:"path,omitempty"`       // For directory type
	Repository  string `json:"repository,omitempty"` // For github type (owner/repo)
	Description string `json:"description,omitempty"`
}

// KnownMarketplaces represents the known_marketplaces.json format (legacy).
type KnownMarketplaces struct {
	Marketplaces []MarketplaceSource `json:"marketplaces"`
}

// MarketplaceMetadata represents the marketplace.json file in a marketplace root.
// Supports both flat format (version/description at top level) and nested format
// (version/description inside a "metadata" object), as used by Claude Code.
type MarketplaceMetadata struct {
	Name        string              `json:"name"`
	Version     string              `json:"version,omitempty"`
	Description string              `json:"description,omitempty"`
	Owner       *Author             `json:"owner,omitempty"`
	Plugins     []MarketplacePlugin `json:"plugins,omitempty"`
	Metadata    *MarketplaceExtra   `json:"metadata,omitempty"`
}

// MarketplaceExtra holds metadata fields that may be nested under "metadata".
type MarketplaceExtra struct {
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

// GetVersion returns version, preferring the top-level field over nested metadata.
func (m *MarketplaceMetadata) GetVersion() string {
	if m.Version != "" {
		return m.Version
	}
	if m.Metadata != nil {
		return m.Metadata.Version
	}
	return ""
}

// GetDescription returns description, preferring the top-level field over nested metadata.
func (m *MarketplaceMetadata) GetDescription() string {
	if m.Description != "" {
		return m.Description
	}
	if m.Metadata != nil {
		return m.Metadata.Description
	}
	return ""
}

// MarketplacePlugin represents a plugin entry in marketplace.json.
type MarketplacePlugin struct {
	Name        string  `json:"name"`
	Source      string  `json:"source"`
	Description string  `json:"description,omitempty"`
	Version     string  `json:"version,omitempty"`
	Author      *Author `json:"author,omitempty"`
	Category    string  `json:"category,omitempty"`
}

// InstallCounts represents the install-counts-cache.json format.
type InstallCounts struct {
	Version   int            `json:"version"`
	FetchedAt string         `json:"fetchedAt"`
	Counts    []InstallCount `json:"counts"`
}

// InstallCount represents install statistics for a plugin.
type InstallCount struct {
	Plugin         string `json:"plugin"` // "name@marketplace"
	UniqueInstalls int    `json:"unique_installs"`
}
