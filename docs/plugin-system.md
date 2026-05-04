# Plugin System

## Overview

GenCode's plugin system bundles **skills, agents, commands, hooks, MCP servers, and LSP servers** into installable, shareable units. The format is compatible with Claude Code's `.claude-plugin/plugin.json`.

Key design principles:

1. **Multi-scope**: Plugins install at user, project, or local scope
2. **Marketplace-based**: Discover and install from directory or GitHub marketplaces
3. **Namespace isolation**: Plugin resources are prefixed to prevent collisions
4. **Lazy loading**: Plugin skills and agents load content on demand
5. **Claude Code compatible**: Can load and run Claude Code plugins

## Plugin Directory Structure

```
plugin-name/
├── .gen-plugin/plugin.json       # Manifest (preferred)
├── .claude-plugin/plugin.json    # Manifest (Claude Code compat)
├── skills/
│   └── skill-name/
│       ├── SKILL.md              # Skill definition (YAML frontmatter + body)
│       ├── scripts/              # Helper scripts
│       ├── references/           # Reference files
│       └── assets/               # Static assets
├── agents/
│   └── agent-name.md             # Agent definition (YAML frontmatter + body)
├── commands/
│   └── command-name.md           # Slash command definition
├── hooks/
│   └── hooks.json                # Event hook definitions
├── .mcp.json                     # MCP server configurations
└── .lsp.json                     # LSP server configurations
```

### Manifest (`plugin.json`)

```json
{
  "name": "my-plugin",
  "version": "1.0.0",
  "description": "What this plugin does",
  "author": { "name": "Name", "email": "email" },
  "homepage": "https://...",
  "repository": "https://...",
  "license": "MIT",
  "keywords": ["tag1", "tag2"],
  "commands": "commands",
  "agents": "agents",
  "skills": "skills",
  "hooks": "hooks/hooks.json",
  "mcpServers": { "server-name": { "command": "...", "args": [] } },
  "lspServers": { "go": { "command": "gopls", "args": ["serve"] } }
}
```

Component path fields (`commands`, `agents`, `skills`, `hooks`, `mcpServers`, `lspServers`) accept:
- `string` - single path or glob
- `[]string` - multiple paths
- `nil` - defaults to `commands/`, `agents/`, `skills/` respectively
- `object` - inline configuration (for hooks, mcpServers, lspServers)

Paths support variable expansion: `${GEN_PLUGIN_ROOT}` and `${CLAUDE_PLUGIN_ROOT}` resolve to the plugin root directory.

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│                         Plugin System Architecture                       │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌─────────────┐      ┌──────────────┐      ┌────────────────────┐     │
│  │ Marketplace  │──────│   Installer   │──────│     Registry       │     │
│  │  Manager     │ find │              │ load │  map[name]*Plugin  │     │
│  └─────────────┘      └──────────────┘      └────────┬───────────┘     │
│        │                                              │                  │
│        │ sync/clone                                   │ GetEnabled()     │
│        ▼                                              ▼                  │
│  ┌─────────────┐                          ┌────────────────────┐        │
│  │  GitHub /    │                          │  Integration Layer │        │
│  │  Directory   │                          │  (integration.go)  │        │
│  │  Sources     │                          └────────┬───────────┘        │
│  └─────────────┘                                    │                    │
│                                     ┌───────────────┼───────────────┐   │
│                                     ▼               ▼               ▼   │
│                              ┌───────────┐  ┌────────────┐  ┌────────┐ │
│                              │  Skills    │  │   Agents   │  │ Hooks/ │ │
│                              │  Loader    │  │   Loader   │  │ MCP/   │ │
│                              │            │  │            │  │ LSP    │ │
│                              └───────────┘  └────────────┘  └────────┘ │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```

## Startup Flow

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          Application Startup                             │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  1. plugin.InitPlugins(ctx, cwd)             integration.go:15           │
│     │                                                                    │
│     ├── DefaultRegistry.Load(ctx, cwd)       registry.go:29             │
│     │   ├── loadEnabledPlugins(cwd)          Read settings.json files   │
│     │   ├── GetPluginDirs(cwd)               Scan plugin directories    │
│     │   │   ├── ~/.gen/plugins/              ScopeUser                  │
│     │   │   ├── .gen/plugins/                ScopeProject               │
│     │   │   └── .gen/plugins-local/          ScopeLocal                 │
│     │   ├── LoadPluginsFromDir(dir, scope)   loader.go:107              │
│     │   │   └── LoadPlugin(path)             loader.go:24               │
│     │   │       ├── loadManifest()           .gen-plugin or .claude-plugin│
│     │   │       └── resolveComponents()      resolver.go                │
│     │   └── LoadInstalledPlugins()           From installed_plugins.json │
│     │                                                                    │
│     ├── LoadClaudePlugins(ctx)               compat.go:17 (optional)    │
│     │   └── Scan ~/.claude/plugins/          If GEN_LOAD_CLAUDE_PLUGINS │
│     │                                                                    │
│     └── LoadFromPath(ctx, path)              If GEN_PLUGIN_DIR set      │
│                                                                          │
│  2. agent.Init(cwd)                          agent/loader.go:216        │
│     ├── ClearPluginAgentPaths()              Reset from previous load   │
│     ├── plugin.GetPluginAgentPaths()         Collect from enabled plugins│
│     │   └── AddPluginAgentPath(path, ns)     Add with namespace         │
│     └── LoadCustomAgents(cwd)                Load all agent .md files   │
│         ├── .gen/agents/                     Project → User → Claude    │
│         └── Plugin agent paths               With namespace prefix      │
│                                                                          │
│  3. skill.Loader.AddPluginPaths(...)         Add plugin skill dirs      │
│     └── skill.Loader.LoadAll()               skill/loader.go:191        │
│         └── getSearchPaths()                 Ordered by priority         │
│                                                                          │
│  4. plugin.MergePluginHooksIntoSettings()    integration.go:110         │
│                                                                          │
│  5. plugin.GetPluginMCPServers()             integration.go:129         │
│     plugin.GetPluginLSPServers()             integration.go:151         │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```

## Scope System

Plugins support three installation scopes, each with distinct directories and default behaviors:

| Scope | Directory | Default State | Use Case |
|-------|-----------|---------------|----------|
| **user** | `~/.gen/plugins/` | Enabled | Personal plugins |
| **project** | `.gen/plugins/` | Disabled | Team plugins (git-tracked) |
| **local** | `.gen/plugins-local/` | Disabled | Local-only (gitignored) |

Installed (marketplace) plugins go to a **cache** subdirectory:
- User scope: `~/.gen/plugins/cache/<plugin-name>/`
- Project scope: `.gen/plugins/<plugin-name>/`
- Local scope: `.gen/plugins-local/<plugin-name>/`

### Enabled State

Plugin enabled/disabled state is stored in `settings.json` under `enabledPlugins`:

```json
{
  "enabledPlugins": {
    "git@cc-plugins": true,
    "jira-tools@acm-workflows-plugins": true,
    "some-plugin": false
  }
}
```

Settings files are read in priority order (later overrides earlier):

1. `~/.claude/settings.json` (Claude user compat)
2. `~/.gen/settings.json` (user)
3. `.claude/settings.json` (Claude project compat)
4. `.gen/settings.json` (project)
5. `.claude/settings.local.json` (Claude project local)
6. `.gen/settings.local.json` (project local)

## Plugin Loading

### LoadPlugin (`loader.go:24`)

```
LoadPlugin(path, scope, source)
    │
    ├── 1. Resolve absolute path, verify directory
    │
    ├── 2. loadManifest(path)
    │      ├── Try .gen-plugin/plugin.json
    │      └── Try .claude-plugin/plugin.json
    │      └── (fallback: infer name from directory)
    │
    └── 3. resolveComponents(manifest, path)
           ├── ResolveCommands()  → Collect *.md files
           ├── ResolveSkills()    → Find dirs with SKILL.md
           ├── ResolveAgents()    → Collect *.md files
           ├── ResolveHooksConfig() → Parse hooks.json or inline
           ├── ResolveMCPServers()  → Parse .mcp.json or inline
           └── ResolveLSPServers()  → Parse .lsp.json or inline
```

### Component Resolution (`resolver.go`)

Each component type has a specific resolution strategy:

| Component | Default Path | Resolution |
|-----------|-------------|------------|
| Commands | `commands/` | Scan for `*.md` files recursively |
| Skills | `skills/` | Scan for subdirectories containing `SKILL.md` |
| Agents | `agents/` | Scan for `*.md` files |
| Hooks | `hooks/hooks.json` | Parse JSON file or inline object |
| MCP | `.mcp.json` | Parse JSON with server configs |
| LSP | `.lsp.json` | Parse JSON with server configs |

## Namespacing

Plugin resources are namespaced to prevent name collisions:

```
┌────────────────────────────────────────────────────────────┐
│  Plugin: "git@cc-plugins"                                  │
│                                                            │
│  Skills:                                                   │
│    git:create-pr    ← namespace "git" + skill "create-pr"  │
│    git:my-prs       ← namespace "git" + skill "my-prs"    │
│                                                            │
│  Agents:                                                   │
│    git:reviewer     ← namespace "git" + agent "reviewer"   │
│                                                            │
│  MCP Servers:                                              │
│    git:fetch-server ← plugin "git" + server "fetch-server" │
│                                                            │
│  Commands:                                                 │
│    (no namespace prefix, direct path-based)                │
│                                                            │
│  Hooks:                                                    │
│    (merged by event name, no namespace)                    │
└────────────────────────────────────────────────────────────┘
```

**Rules:**
- **Skills**: `{plugin-name}:{skill-name}` — set via `defaultNamespace` in `skill/loader.go:268`
- **Agents**: `{plugin-name}:{agent-name}` — set via namespace in `agent/loader.go:120`, only if agent name doesn't already contain `:`
- **MCP servers**: `{plugin-name}:{server-name}` — set in `registry.go:374`
- **LSP servers**: No namespace (keyed by language name)
- **Hooks**: No namespace (merged into global hooks by event name)

## Skill Loading from Plugins

```
┌──────────────────────────────────────────────────────────────────────────┐
│                    Skill Search Path Priority                            │
│                    (lowest → highest priority)                           │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  1. ~/.claude/skills/                    ScopeClaudeUser (lowest)        │
│  2. User plugins (installed_plugins.json) ScopeUserPlugin                │
│  3. ~/.gen/skills/                       ScopeUser                       │
│  4. .claude/skills/                      ScopeClaudeProject              │
│  5. Project plugins (installed_plugins.json) ScopeProjectPlugin          │
│  6. .gen/skills/                         ScopeProject (highest)          │
│                                                                          │
│  Higher priority scopes override lower ones with the same FullName.      │
│                                                                          │
│  Lazy loading: Only YAML frontmatter is parsed at startup.               │
│  Skill instructions (body) are loaded on first GetInstructions() call.   │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```

The skill loader discovers plugin skills via two paths:

1. **installed_plugins.json** (`skill/loader.go:145`): Reads the installed plugins file, extracts plugin name from key (before `@`), looks for `{installPath}/skills/` subdirectory
2. **Additional paths** (`skill/loader.go:48`): Plugin paths added explicitly via `AddPluginPath()`, inserted at correct priority position

## Agent Loading from Plugins

```
┌──────────────────────────────────────────────────────────────────────────┐
│                    Agent Search Path Priority                            │
│                    (first match wins — no override)                      │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  1. .gen/agents/                         Project (preferred)             │
│  2. ~/.gen/agents/                       User (preferred)                │
│  3. .claude/agents/                      Project Claude compat           │
│  4. ~/.claude/agents/                    User Claude compat              │
│  5. Plugin agent paths                   From enabled plugins            │
│                                                                          │
│  Agent init flow:                                                        │
│  agent.Init(cwd)                                                         │
│    ├── ClearPluginAgentPaths()                                           │
│    ├── for pp := range plugin.GetPluginAgentPaths()                      │
│    │       AddPluginAgentPath(pp.Path, pp.Namespace)                     │
│    └── LoadCustomAgents(cwd)                                             │
│          └── for each path: loadAgentsFromDirWithNamespace()             │
│                └── loadAgentFromFileWithNamespace(file, namespace)        │
│                      ├── parseAgentFile() → metadata only (lazy body)    │
│                      ├── if namespace != "" && !contains(":"):           │
│                      │     config.Name = namespace + ":" + config.Name   │
│                      └── DefaultRegistry.Register(config)                │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```

## Marketplace System

### Marketplace Configuration

Marketplaces are registered in `~/.gen/plugins/known_marketplaces.json`:

```json
{
  "marketplaces": [
    {
      "name": "cc-plugins",
      "type": "directory",
      "path": "/Users/user/.claude"
    },
    {
      "name": "acm-workflows-plugins",
      "type": "github",
      "repository": "stolostron/acm-workflows"
    }
  ]
}
```

### Marketplace Types

| Type | Source | Sync Method |
|------|--------|-------------|
| **directory** | Local filesystem path | Direct access (no sync needed) |
| **github** | GitHub `owner/repo` | `git clone --depth 1` → `~/.gen/plugins/marketplaces/` |

### Plugin Discovery in Marketplace

When listing available plugins, the marketplace manager searches:

```
{marketplace-root}/plugins/{plugin-name}/   (preferred)
{marketplace-root}/{plugin-name}/           (fallback)
{marketplace-root}/Claude/plugins/{plugin-name}/  (Claude compat)
```

A valid plugin must have either a manifest file (`.gen-plugin/plugin.json` or `.claude-plugin/plugin.json`) or at least one of `skills/`, `commands/`, or `agents/` directories.

## Installation Flow

```
┌──────────────────────────────────────────────────────────────────────────┐
│                        Plugin Installation                               │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  gen plugin install git@cc-plugins --scope user                          │
│  (or via TUI plugin selector)                                            │
│                                                                          │
│  Installer.Install(ctx, "git@cc-plugins", ScopeUser)                    │
│    │                                                                     │
│    ├── 1. ParsePluginRef("git@cc-plugins")                               │
│    │      → name="git", marketplace="cc-plugins"                         │
│    │                                                                     │
│    ├── 2. Find marketplace source                                        │
│    │      → MarketplaceSource{Type: "directory", Path: "~/.claude"}      │
│    │                                                                     │
│    ├── 3. Determine install directory                                     │
│    │      → ~/.gen/plugins/cache/git/                                    │
│    │                                                                     │
│    ├── 4. Install (based on source type)                                  │
│    │      ├── directory: copyDir(srcPath, pluginPath)                    │
│    │      └── github:                                                    │
│    │          ├── marketplaceManager.Sync() → git clone/pull             │
│    │          └── copyDir(srcPath, pluginPath)                           │
│    │                                                                     │
│    ├── 5. addToInstalled(scope, pluginInfo)                               │
│    │      → Write to installed_plugins.json (v2 format)                  │
│    │                                                                     │
│    ├── 6. LoadPlugin(pluginPath) → registry.Register()                   │
│    │                                                                     │
│    └── 7. registry.Enable("git@cc-plugins", ScopeUser)                   │
│           → Write to settings.json                                       │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```

### `installed_plugins.json` (v2 format)

```json
{
  "version": 2,
  "plugins": {
    "git@cc-plugins": [
      {
        "scope": "user",
        "installPath": "/Users/user/.gen/plugins/cache/git",
        "version": "1.2.0",
        "installedAt": "2025-01-15T10:30:00Z",
        "lastUpdated": "2025-01-15T10:30:00Z",
        "gitCommitSha": "abc123"
      }
    ]
  }
}
```

## Integration Layer (`integration.go`)

The integration layer bridges the plugin registry with other subsystems:

```go
// Collect resource paths from enabled plugins
GetPluginSkillPaths()   → []PluginPath{Path, Namespace, Scope}
GetPluginAgentPaths()   → []PluginPath{Path, Namespace, Scope}
GetPluginCommandPaths() → []PluginPath{Path, Namespace, Scope}

// Server configurations (namespaced)
GetPluginMCPServers()   → []PluginMCPServer{Name: "plugin:server", Config, Scope}
GetPluginLSPServers()   → []PluginLSPServer{Name: "go", Config, Scope}

// Hooks (merged into application settings)
GetPluginHooks()                      → map[event][]config.Hook
MergePluginHooksIntoSettings(settings) → Append plugin hooks to settings.Hooks
```

## Claude Code Compatibility

When `GEN_LOAD_CLAUDE_PLUGINS=true`:

1. Scans `~/.claude/plugins/cache/` and `~/.claude/plugins/`
2. Loads plugins with `@claude` source suffix
3. Reads enabled state from `~/.claude/settings.json`
4. `${CLAUDE_PLUGIN_ROOT}` expands to the same value as `${GEN_PLUGIN_ROOT}`
5. `.claude-plugin/plugin.json` is accepted alongside `.gen-plugin/plugin.json`

## CLI Commands

```
gen plugin list                       # List installed plugins with status
gen plugin install <name>@<mkt>       # Install from marketplace
gen plugin uninstall <name>           # Remove plugin
gen plugin enable <name>              # Enable plugin
gen plugin disable <name>             # Disable plugin
gen plugin validate [path]            # Validate plugin structure
gen plugin info <name>                # Show plugin details

Flags:
  --scope, -s   Installation scope: user (default), project, local
```

## TUI Plugin Selector

The TUI provides a three-tab plugin management interface:

| Tab | Content |
|-----|---------|
| **Installed** | Plugins grouped by scope, toggle enable/disable, uninstall |
| **Discover** | Browse available plugins from all marketplaces, install |
| **Marketplaces** | Add/remove/sync marketplace sources |

## Key Data Structures

```go
// Plugin - a loaded plugin with resolved components
type Plugin struct {
    Manifest   Manifest       // Parsed plugin.json
    Path       string         // Absolute path to plugin root
    Scope      Scope          // user / project / local
    Enabled    bool           // Whether active
    Source     string         // "name@marketplace"
    Components Components     // Resolved paths
    Errors     []string       // Load errors (non-fatal)
}

// Components - resolved resource paths
type Components struct {
    Commands []string                    // Paths to .md files
    Skills   []string                    // Paths to skill directories
    Agents   []string                    // Paths to .md files
    Hooks    *HooksConfig                // Parsed hooks config
    MCP      map[string]MCPServerConfig  // MCP server configs
    LSP      map[string]LSPServerConfig  // LSP server configs
}

// Registry - global plugin registry (thread-safe)
type Registry struct {
    plugins map[string]*Plugin  // key: "name@marketplace"
}
```

## Error Handling

The plugin system uses **non-fatal error handling** throughout:

- Individual plugin load failures don't stop the registry from loading other plugins
- Missing manifest falls back to inferring plugin name from directory name
- Missing component directories are silently skipped
- Errors are stored in `Plugin.Errors` and displayed in the TUI detail view
- Settings file read failures fall back to empty defaults

## See Also

- [Skill System](skill-system.md) — Skills loaded from plugins
- [Subagent System](subagent-system.md) — Agents loaded from plugins
- [MCP Servers](mcp-servers.md) — MCP servers bundled in plugins
- [System Prompt](system-prompt.md) — Slot/Section model and progressive loading strategy
