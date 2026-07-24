package subagent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/markdown"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// PluginAgentPath represents a plugin agent path with namespace metadata.
type PluginAgentPath struct {
	Path      string
	Namespace string
	IsProject bool
}

// agentSearchPath represents an agent search location with optional namespace.
type agentSearchPath struct {
	path      string
	namespace string // Default namespace for agents in this path (from plugin)
	source    string
}

// LoadCustomAgents replaces the package registry with custom definitions from
// the standard locations. Production initialization uses loadCustomAgents so it
// can include plugin paths and install the complete registry atomically.
func LoadCustomAgents(cwd string) {
	registry := NewRegistry()
	loadCustomAgents(cwd, registry, nil)
	setDefaultRegistry(registry)
}

func loadCustomAgents(cwd string, registry *Registry, pluginPaths func() []PluginAgentPath) {
	homeDir, _ := os.UserHomeDir()

	priorityOrdered := []agentSearchPath{
		{path: filepath.Join(confdir.Dir(cwd), "agents")},
		{path: filepath.Join(confdir.Dir(homeDir), "agents")},
		{path: filepath.Join(cwd, ".claude", "agents")},
		{path: filepath.Join(homeDir, ".claude", "agents")},
	}
	if pluginPaths != nil {
		for _, plugin := range pluginPaths() {
			source := "user-plugin"
			if plugin.IsProject {
				source = "project-plugin"
			}
			priorityOrdered = append(priorityOrdered, agentSearchPath{path: plugin.Path, namespace: plugin.Namespace, source: source})
		}
	}

	for i := len(priorityOrdered) - 1; i >= 0; i-- {
		sp := priorityOrdered[i]
		loadAgentsFromDirWithNamespace(registry, sp.path, sp.namespace, sp.source)
	}
}

// loadAgentsFromDirWithNamespace loads agents with an optional namespace prefix.
// The path can be either a directory (scanned for .md files) or a direct file path.
func loadAgentsFromDirWithNamespace(registry *Registry, path string, namespace string, source ...string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}

	// If path is a file, load it directly
	if !info.IsDir() {
		if strings.HasSuffix(path, ".md") {
			loadAgentFromFileWithNamespace(registry, path, namespace, source...)
		}
		return
	}

	// Path is a directory, scan for .md files
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}

		filePath := filepath.Join(path, name)
		loadAgentFromFileWithNamespace(registry, filePath, namespace, source...)
	}
}

// loadAgentFromFileWithNamespace loads an agent with optional namespace.
func loadAgentFromFileWithNamespace(registry *Registry, filePath string, namespace string, source ...string) {
	config, err := parseAgentFile(filePath)
	if err != nil {
		log.Logger().Debug("Failed to parse agent file",
			zap.String("path", filePath),
			zap.Error(err))
		return
	}

	if config != nil {
		if namespace != "" {
			localName := config.Name
			if _, suffix, ok := strings.Cut(localName, ":"); ok {
				localName = suffix
			}
			config.Name = namespace + ":" + localName
		}
		if len(source) > 0 && source[0] != "" {
			config.Source = source[0]
		}

		registry.Register(config)
		log.Logger().Info("Loaded custom agent",
			zap.String("name", config.Name),
			zap.String("source", filePath))
	}
}

// frontmatterAliases are alternate key spellings accepted in agent
// frontmatter: `tools` is Claude Code's key, the hyphenated forms are the
// spellings docs/guides/writing-a-subagent.md documents. Canonical keys on
// AgentConfig win when both are present.
type frontmatterAliases struct {
	Tools          ToolList       `yaml:"tools"`
	AllowedTools   ToolList       `yaml:"allowed-tools"`
	PermissionMode PermissionMode `yaml:"permission-mode"`
	MaxSteps       int            `yaml:"max_steps"`
}

func (a frontmatterAliases) applyTo(config *AgentConfig) {
	if config.AllowTools == nil {
		if a.AllowedTools != nil {
			config.AllowTools = a.AllowedTools
		} else if a.Tools != nil {
			config.AllowTools = a.Tools
		}
	}
	if config.PermissionMode == "" && a.PermissionMode != "" {
		config.PermissionMode = a.PermissionMode
	}
	if config.MaxSteps <= 0 && a.MaxSteps > 0 {
		config.MaxSteps = a.MaxSteps
	}
}

// parseAgentFile parses an AGENT.md file with YAML frontmatter.
func parseAgentFile(filePath string) (*AgentConfig, error) {
	frontmatter, _, err := markdown.ParseFrontmatterFile(filePath)
	if err != nil {
		return nil, err
	}
	if frontmatter == "" {
		return nil, nil
	}

	var config AgentConfig
	if err := yaml.Unmarshal([]byte(frontmatter), &config); err != nil {
		return nil, err
	}
	var aliases frontmatterAliases
	if err := yaml.Unmarshal([]byte(frontmatter), &aliases); err == nil {
		aliases.applyTo(&config)
	}

	config.PermissionMode = NormalizePermissionMode(string(config.PermissionMode))

	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		config.Name = strings.TrimSuffix(filepath.Base(filePath), ".md")
	}
	if config.Model == "" {
		config.Model = "inherit"
	}
	if config.MaxSteps <= 0 {
		config.MaxSteps = defaultMaxSteps
	}
	if config.PermissionMode == "" {
		config.PermissionMode = PermissionDefault
	}

	// Body is lazily loaded via GetSystemPrompt()
	config.SourceFile = filePath

	if config.Source == "" {
		homeDir, _ := os.UserHomeDir()
		switch {
		case strings.HasPrefix(filePath, filepath.Join(confdir.Dir(homeDir), "agents")),
			strings.HasPrefix(filePath, filepath.Join(homeDir, ".claude", "agents")):
			config.Source = "user"
		default:
			config.Source = "project"
		}
	}

	return &config, nil
}

// LoadAgentSystemPrompt loads just the system prompt (body) from an agent file.
func LoadAgentSystemPrompt(filePath string) string {
	_, body, err := markdown.ParseFrontmatterFile(filePath)
	if err != nil {
		log.Logger().Debug("Failed to read agent file for system prompt",
			zap.String("path", filePath),
			zap.Error(err))
		return ""
	}
	return body
}
