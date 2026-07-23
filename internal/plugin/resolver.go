package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/setting"
)

const (
	// EnvPluginRoot is the variable for plugin root directory
	EnvPluginRoot = "SAN_PLUGIN_ROOT"

	// EnvClaudePluginRoot is Claude Code's plugin root variable
	EnvClaudePluginRoot = "CLAUDE_PLUGIN_ROOT"
)

// ExpandPluginRoot replaces ${SAN_PLUGIN_ROOT} (and the ${CLAUDE_PLUGIN_ROOT}
// alias) in the given string with the actual plugin path.
func ExpandPluginRoot(s string, pluginPath string) string {
	s = strings.ReplaceAll(s, "${"+EnvPluginRoot+"}", pluginPath)
	s = strings.ReplaceAll(s, "${"+EnvClaudePluginRoot+"}", pluginPath)
	return s
}

// ResolvePaths resolves paths from manifest field, which can be:
// - string: single path or glob pattern
// - []string: multiple paths
// - nil: use default paths
func ResolvePaths(field any, pluginPath string, defaults []string) []string {
	if field == nil {
		// Return default paths that exist
		var result []string
		for _, d := range defaults {
			path := filepath.Join(pluginPath, d)
			if _, err := os.Stat(path); err == nil {
				result = append(result, path)
			}
		}
		return result
	}

	switch v := field.(type) {
	case string:
		path := ExpandPluginRoot(v, pluginPath)
		if !filepath.IsAbs(path) {
			path = filepath.Join(pluginPath, path)
		}
		return []string{path}
	case []any:
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				path := ExpandPluginRoot(s, pluginPath)
				if !filepath.IsAbs(path) {
					path = filepath.Join(pluginPath, path)
				}
				result = append(result, path)
			}
		}
		return result
	case []string:
		var result []string
		for _, s := range v {
			path := ExpandPluginRoot(s, pluginPath)
			if !filepath.IsAbs(path) {
				path = filepath.Join(pluginPath, path)
			}
			result = append(result, path)
		}
		return result
	default:
		return nil
	}
}

// ResolveCommands resolves command file paths from the plugin.
func ResolveCommands(manifest *Manifest, pluginPath string) []string {
	paths := ResolvePaths(manifest.Commands, pluginPath, []string{"commands"})
	return collectMarkdownFiles(paths)
}

// ResolveSkills resolves skill directory paths from the plugin.
func ResolveSkills(manifest *Manifest, pluginPath string) []string {
	// Default: skills/ directory
	paths := ResolvePaths(manifest.Skills, pluginPath, []string{"skills"})

	var skills []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			// Scan for skill directories (containing SKILL.md or skill.md)
			entries, err := os.ReadDir(p)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() {
					skillDir := filepath.Join(p, entry.Name())
					if hasSkillFile(skillDir) {
						skills = append(skills, skillDir)
					}
				}
			}
		}
	}
	return skills
}

// collectMarkdownFiles collects markdown files from a list of paths.
// Each path can be a file or directory.
func collectMarkdownFiles(paths []string) []string {
	var result []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			if files, err := scanMarkdownFiles(p); err == nil {
				result = append(result, files...)
			}
		} else if strings.HasSuffix(p, ".md") {
			result = append(result, p)
		}
	}
	return result
}

// ResolveHooksConfig resolves hooks configuration from the plugin.
// Can be a path to hooks.json, inline config, or default location.
func ResolveHooksConfig(field any, pluginPath string) *HooksConfig {
	if field == nil {
		// Try default location: hooks/hooks.json
		defaultPath := filepath.Join(pluginPath, "hooks", "hooks.json")
		if _, err := os.Stat(defaultPath); err == nil {
			return loadHooksFile(defaultPath, pluginPath)
		}
		return nil
	}

	switch v := field.(type) {
	case string:
		// Path to hooks file
		path := ExpandPluginRoot(v, pluginPath)
		if !filepath.IsAbs(path) {
			path = filepath.Join(pluginPath, path)
		}
		return loadHooksFile(path, pluginPath)
	case map[string]any:
		// Inline hooks configuration
		return parseHooksMap(v, pluginPath)
	default:
		return nil
	}
}

// ResolveMCPServers resolves MCP server configurations from the plugin.
func ResolveMCPServers(field any, pluginPath string) map[string]MCPServerConfig {
	return resolveConfigMap(field, pluginPath, ".mcp.json", loadMCPFile, parseMCPMap)
}

// ResolveLSPServers resolves LSP server configurations from the plugin.
func ResolveLSPServers(field any, pluginPath string) map[string]LSPServerConfig {
	return resolveConfigMap(field, pluginPath, ".lsp.json", loadLSPFile, parseLSPMap)
}

// resolveConfigMap is a generic resolver for config file or inline map fields.
func resolveConfigMap[T any](
	field any,
	pluginPath string,
	defaultFile string,
	loadFile func(string, string) T,
	parseMap func(map[string]any, string) T,
) T {
	var zero T
	if field == nil {
		defaultPath := filepath.Join(pluginPath, defaultFile)
		if _, err := os.Stat(defaultPath); err == nil {
			return loadFile(defaultPath, pluginPath)
		}
		return zero
	}

	switch v := field.(type) {
	case string:
		path := ExpandPluginRoot(v, pluginPath)
		if !filepath.IsAbs(path) {
			path = filepath.Join(pluginPath, path)
		}
		return loadFile(path, pluginPath)
	case map[string]any:
		return parseMap(v, pluginPath)
	default:
		return zero
	}
}

// hasSkillFile checks if a directory contains SKILL.md or skill.md.
func hasSkillFile(dir string) bool {
	for _, name := range []string{"SKILL.md", "skill.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// scanMarkdownFiles recursively scans a directory for markdown files.
func scanMarkdownFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// loadHooksFile loads hooks configuration from a JSON file.
func loadHooksFile(path string, pluginPath string) *HooksConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	return parseHooksMap(raw, pluginPath)
}

// parseHooksMap parses hooks configuration from a map.
func parseHooksMap(m map[string]any, pluginPath string) *HooksConfig {
	hooksField, ok := m["hooks"]
	if !ok {
		return nil
	}
	hooksMap, ok := hooksField.(map[string]any)
	if !ok {
		return nil
	}

	hooksConfig := &HooksConfig{
		Hooks: make(map[string][]HookMatcher),
	}

	for eventName, matchersAny := range hooksMap {
		matchersSlice, ok := matchersAny.([]any)
		if !ok {
			continue
		}
		var matchers []HookMatcher
		for _, matcherAny := range matchersSlice {
			matcherMap, ok := matcherAny.(map[string]any)
			if !ok {
				continue
			}
			matcher := HookMatcher{}
			if m, ok := matcherMap["matcher"].(string); ok {
				matcher.Matcher = m
			}
			if hooksAny, ok := matcherMap["hooks"].([]any); ok {
				for _, hookAny := range hooksAny {
					hookMap, ok := hookAny.(map[string]any)
					if !ok {
						continue
					}
					cmd := setting.HookCmd{}
					if t, ok := hookMap["type"].(string); ok {
						cmd.Type = t
					}
					if c, ok := hookMap["command"].(string); ok {
						cmd.Command = ExpandPluginRoot(c, pluginPath)
					}
					if p, ok := hookMap["prompt"].(string); ok {
						cmd.Prompt = p
					}
					if u, ok := hookMap["url"].(string); ok {
						cmd.URL = u
					}
					if cond, ok := hookMap["if"].(string); ok {
						cmd.If = cond
					}
					if sh, ok := hookMap["shell"].(string); ok {
						cmd.Shell = sh
					}
					if m, ok := hookMap["model"].(string); ok {
						cmd.Model = m
					}
					if a, ok := hookMap["async"].(bool); ok {
						cmd.Async = a
					}
					if a, ok := hookMap["asyncRewake"].(bool); ok {
						cmd.AsyncRewake = a
					}
					if t, ok := hookMap["timeout"].(float64); ok {
						cmd.Timeout = int(t)
					}
					if s, ok := hookMap["statusMessage"].(string); ok {
						cmd.StatusMessage = s
					}
					if o, ok := hookMap["once"].(bool); ok {
						cmd.Once = o
					}
					if headers, ok := hookMap["headers"].(map[string]any); ok {
						cmd.Headers = make(map[string]string, len(headers))
						for k, v := range headers {
							if s, ok := v.(string); ok {
								cmd.Headers[k] = s
							}
						}
					}
					if envs, ok := hookMap["allowedEnvVars"].([]any); ok {
						for _, env := range envs {
							if s, ok := env.(string); ok {
								cmd.AllowedEnvVars = append(cmd.AllowedEnvVars, s)
							}
						}
					}
					matcher.Hooks = append(matcher.Hooks, cmd)
				}
			}
			matchers = append(matchers, matcher)
		}
		hooksConfig.Hooks[eventName] = matchers
	}
	return hooksConfig
}

// loadMCPFile loads MCP server configurations from a JSON file.
func loadMCPFile(path string, pluginPath string) map[string]MCPServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	// Handle both direct server map and wrapped mcpServers field
	if servers, ok := raw["mcpServers"].(map[string]any); ok {
		return parseMCPMap(servers, pluginPath)
	}
	return parseMCPMap(raw, pluginPath)
}

// parseMCPMap parses MCP server configurations from a map.
func parseMCPMap(m map[string]any, pluginPath string) map[string]MCPServerConfig {
	result := make(map[string]MCPServerConfig)
	for name, configAny := range m {
		configMap, ok := configAny.(map[string]any)
		if !ok {
			continue
		}
		config := MCPServerConfig{}
		if t, ok := configMap["type"].(string); ok {
			config.Type = t
		}
		if c, ok := configMap["command"].(string); ok {
			config.Command = ExpandPluginRoot(c, pluginPath)
		}
		if args, ok := configMap["args"].([]any); ok {
			for _, arg := range args {
				if s, ok := arg.(string); ok {
					config.Args = append(config.Args, ExpandPluginRoot(s, pluginPath))
				}
			}
		}
		if env, ok := configMap["env"].(map[string]any); ok {
			config.Env = make(map[string]string)
			for k, v := range env {
				if s, ok := v.(string); ok {
					config.Env[k] = ExpandPluginRoot(s, pluginPath)
				}
			}
		}
		if url, ok := configMap["url"].(string); ok {
			config.URL = url
		}
		if headers, ok := configMap["headers"].(map[string]any); ok {
			config.Headers = make(map[string]string)
			for k, v := range headers {
				if s, ok := v.(string); ok {
					config.Headers[k] = s
				}
			}
		}
		result[name] = config
	}
	return result
}

// loadLSPFile loads LSP server configurations from a JSON file.
func loadLSPFile(path string, pluginPath string) map[string]LSPServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	return parseLSPMap(raw, pluginPath)
}

// parseLSPMap parses LSP server configurations from a map.
func parseLSPMap(m map[string]any, pluginPath string) map[string]LSPServerConfig {
	result := make(map[string]LSPServerConfig)
	for name, configAny := range m {
		configMap, ok := configAny.(map[string]any)
		if !ok {
			continue
		}
		config := LSPServerConfig{}
		if c, ok := configMap["command"].(string); ok {
			config.Command = ExpandPluginRoot(c, pluginPath)
		}
		if args, ok := configMap["args"].([]any); ok {
			for _, arg := range args {
				if s, ok := arg.(string); ok {
					config.Args = append(config.Args, ExpandPluginRoot(s, pluginPath))
				}
			}
		}
		if ext, ok := configMap["extensionToLanguage"].(map[string]any); ok {
			config.ExtensionToLanguage = make(map[string]string)
			for k, v := range ext {
				if s, ok := v.(string); ok {
					config.ExtensionToLanguage[k] = s
				}
			}
		}
		result[name] = config
	}
	return result
}
