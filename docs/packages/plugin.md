---
package: github.com/genai-io/san/internal/plugin
layer: feature
---

# plugin

> 中文版本：[`plugin.zh.md`](plugin.zh.md)

Plugin loader, installer, marketplace, and aggregator. A plugin is a
single package (directory) that may contribute skills, subagent
definitions, slash commands, MCP servers, hooks, and env vars — this
package discovers them, enables/disables them, and exposes their
contributions to the consuming feature packages.

## Purpose

San's "everything else" extension surface. Plugins are how a user
installs a bundle of skills + agents + commands + MCP servers + hooks in
one shot. This package handles install/uninstall, marketplace lookup,
load order, and the cross-cutting callbacks that let `skill`, `subagent`,
`command`, `mcp`, `hook`, and `setting` see what each enabled plugin
contributes without importing `plugin` directly.

## Contract

The host reads plugin paths, MCP servers and hooks from its own `*Registry`.

```go
package plugin

// Registry is the opaque handle to the loaded plugin set plus per-scope
// enabled state. Type exported, fields unexported.
type Registry struct { /* internal fields */ }

// Loading
func (r *Registry) Load(ctx context.Context, cwd string) error
func (r *Registry) LoadFromPath(ctx context.Context, path string) error
func (r *Registry) LoadClaudePlugins(ctx context.Context) error

// Query
func (r *Registry) Get(name string) (*Plugin, bool)
func (r *Registry) List() []*Plugin
func (r *Registry) ListEnabled() []*Plugin
func (r *Registry) Count() int
func (r *Registry) EnabledCount() int
func (r *Registry) GetByScope(scope Scope) []*Plugin

// Mutation
func (r *Registry) Enable(name string, scope Scope) error
func (r *Registry) Disable(name string, scope Scope) error
func (r *Registry) Register(p *Plugin)
func (r *Registry) Unregister(name string)

// Installer construction (package-level free function)
func NewInstaller(reg *Registry, cwd string) *Installer

// Read from the host's registry (TUI default or ACP session).
func (r *Registry) AgentPaths() []PluginPath
func (r *Registry) SkillPaths() []PluginPath
func (r *Registry) CommandPaths() []PluginPath
func (r *Registry) MCPServers() []PluginMCPServer
func (r *Registry) Hooks() map[string][]setting.Hook
func PluginEnv(ctx context.Context) []string

// Plugin root for the current invocation
func WithRoot(ctx context.Context, root string) context.Context
func (r *Registry) RootForPath(path string) string

// Package-level access
func Initialize(ctx context.Context, opts Options) error
func Default() *Registry
func SetDefaultRegistry(r *Registry)  // test-only
func ResetDefaultRegistry()           // test-only
```

## Internals

- `Registry` (`registry.go`) — `Plugin` map keyed by name, enable state
  per scope (user / project).
- `loader.go` — discovers `.san/plugins/`, `~/.san/plugins/`,
  `.claude/plugins/`, `~/.claude/plugins/`.
- `installer.go` — install/uninstall logic, dependency check, version
  pin (~13 KB).
- `marketplace.go` — registry-of-registries lookup (where to find
  plugin sources).
- `resolver.go` — name → install spec resolution.
- `integration.go` — the cross-domain callback wiring.

## Lifecycle

- Construction: load the registry before wiring enabled plugin paths into the feature packages.
- Reload: enabling/disabling a plugin triggers a reload of the
  affected feature packages (commands/skills/subagents/MCP).

## Marketplace & install flow

A marketplace is a catalog. Its `marketplace.json` *declares* plugins;
each plugin's `source` says where its content actually lives — a path
inside the marketplace repo, or its own external repo (Claude Code's
model). "N available" is the count of declared plugins, independent of
what is cloned on disk.

```mermaid
flowchart TD
    A["/plugin → Marketplaces → Add"] --> B["AddGitHub / AddDirectory<br/>record source in known_marketplaces.json"]
    B --> C["Sync: git clone --depth 1<br/>→ ~/.san/plugins/marketplaces/&lt;id&gt;"]
    C --> D{"marketplace.json<br/>has plugins[] ?"}
    D -- yes --> E["MarketplacePlugins:<br/>declared plugins (manifest)"]
    D -- no --> F["scan vendored subdirs<br/>(fallback)"]
    E --> G["Discover / Browse list<br/>N available = count"]
    F --> G
    G --> H["Install: name@marketplace + scope"]
    H --> I{"plugin source type ?"}
    I -- "path (in repo)" --> J["ResolveLocalPluginPath<br/>inside the marketplace clone"]
    I -- "github / url / git-subdir" --> K["fetchExternalPlugin:<br/>git clone the plugin's own repo<br/>honoring ref/sha, strip .git"]
    J --> L["copyDir → scope dir"]
    K --> L
    L --> M["record in installed_plugins.json"]
    M --> N["LoadPlugin → Register → Enable"]
    N --> O["resolve components:<br/>skills / agents / commands / hooks / mcp / lsp"]
    O --> P["app feeds them to the feature packages<br/>skill / subagent / command / mcp / hook"]
```

**Plugin `source` formats** (in `marketplace.json` `plugins[]`):

| Form | Example | Content fetched from |
|------|---------|----------------------|
| relative path (string) | `"./plugins/foo"` | a directory inside the marketplace repo |
| `github` | `{"source":"github","repo":"owner/repo","ref?":"","sha?":""}` | that GitHub repo |
| `url` (alias `git`) | `{"source":"url","url":"https://host/p.git","ref?":""}` | that git repo (any host) |
| `git-subdir` | `{"source":"git-subdir","url":"…","path":"sub/dir"}` | a subdirectory of that repo |
| `npm` | `{"source":"npm","package":"@scope/p"}` | npm (parsed; install not yet supported) |

**Scope → install dir:** user → `~/.san/plugins/cache/`, project →
`.san/plugins/`, local → `.san/plugins-local/`. Enable state lives in
the scope's `settings.json` under `enabledPlugins`.

## Tests

```
internal/plugin/plugin_test.go              — large suite covering load /
                                              install / enable / contributions.
internal/plugin/marketplace_manifest_test.go — manifest source parsing,
                                              available listing, path install.
```

## See Also

- Code: `internal/plugin/`
- Consumers: [`packages/skill.md`](skill.md), [`packages/subagent.md`](subagent.md), [`packages/command.md`](command.md), [`packages/mcp.md`](mcp.md), [`packages/hook.md`](hook.md), [`packages/setting.md`](setting.md)
- Concepts: [`concepts/extension-model.md`](../concepts/extension-model.md)
- Layer: `feature`
