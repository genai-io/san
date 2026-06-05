package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/v2/internal/confdir"
	"github.com/genai-io/san/v2/internal/markdown"

	"gopkg.in/yaml.v3"
)

// installedPluginsData represents the installed_plugins.json structure.
type installedPluginsData struct {
	Version int                        `json:"version"`
	Plugins map[string][]pluginInstall `json:"plugins"`
}

// pluginInstall represents a single plugin installation.
type pluginInstall struct {
	Scope       string `json:"scope"`       // "user" or "project"
	InstallPath string `json:"installPath"` // Full path to plugin
	Version     string `json:"version"`
}

// Loader handles loading skills from multiple directories.
type loader struct {
	cwd string // Current working directory for project-level skills
}

// searchPath represents a skill search location with optional namespace.
type searchPath struct {
	path      string
	scope     SkillScope
	namespace string // Default namespace for skills in this path (from plugin dir)
}

// newLoader creates a new skill loader.
func newLoader(cwd string) *loader {
	return &loader{
		cwd: cwd,
	}
}

// getSearchPaths returns skill directories in priority order (lowest to highest).
// Note: .claude/plugins/ loading is removed - plugins are handled by the plugin system.
func (l *loader) getSearchPaths() []searchPath {
	homeDir, _ := os.UserHomeDir()
	var paths []searchPath

	// 1. ~/.claude/skills/ (Claude user compat - lowest priority)
	paths = append(paths, searchPath{
		path:  filepath.Join(homeDir, ".claude", "skills"),
		scope: ScopeClaudeUser,
	})

	// 2. User plugins from ~/.san/plugins/installed_plugins.json
	paths = append(paths, l.getPluginPaths(
		filepath.Join(confdir.Dir(homeDir), "plugins"),
		ScopeUserPlugin,
	)...)

	// 3. ~/.san/skills/ (User level)
	paths = append(paths, searchPath{
		path:  filepath.Join(confdir.Dir(homeDir), "skills"),
		scope: ScopeUser,
	})

	// 4. .claude/skills/ (Claude project compat)
	paths = append(paths, searchPath{
		path:  filepath.Join(l.cwd, ".claude", "skills"),
		scope: ScopeClaudeProject,
	})

	// 5. Project plugins from .san/plugins/installed_plugins.json
	paths = append(paths, l.getPluginPaths(
		filepath.Join(confdir.Dir(l.cwd), "plugins"),
		ScopeProjectPlugin,
	)...)

	// 6. .san/skills/ (Project level - highest priority)
	paths = append(paths, searchPath{
		path:  filepath.Join(confdir.Dir(l.cwd), "skills"),
		scope: ScopeProject,
	})

	return paths
}

// getPluginPaths discovers plugin skill directories from installed_plugins.json.
// Plugin skills inherit namespace from their plugin name (before @).
func (l *loader) getPluginPaths(pluginsDir string, scope SkillScope) []searchPath {
	var paths []searchPath

	// Read installed_plugins.json
	configPath := filepath.Join(pluginsDir, "installed_plugins.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return paths // File doesn't exist
	}

	var config installedPluginsData
	if err := json.Unmarshal(data, &config); err != nil {
		return paths // Invalid JSON
	}

	// Process each installed plugin
	for pluginKey, installs := range config.Plugins {
		if len(installs) == 0 {
			continue
		}

		// Use the first (most recent) installation
		install := installs[0]

		// Extract plugin name from key (format: "plugin-name@marketplace")
		pluginName := pluginKey
		if idx := strings.Index(pluginKey, "@"); idx > 0 {
			pluginName = pluginKey[:idx]
		}

		// Check if skills directory exists in the install path
		skillsDir := filepath.Join(install.InstallPath, "skills")
		if _, err := os.Stat(skillsDir); err == nil {
			paths = append(paths, searchPath{
				path:      skillsDir,
				scope:     scope,
				namespace: pluginName, // Plugin name becomes default namespace
			})
		}
	}

	return paths
}

// loadAll loads all skills from all directories.
// Higher priority scopes override lower priority ones with the same name.
func (l *loader) loadAll() (map[string]*Skill, error) {
	skills := make(map[string]*Skill)

	for _, sp := range l.getSearchPaths() {
		if _, err := os.Stat(sp.path); os.IsNotExist(err) {
			continue
		}

		// Walk the skills directory
		err := filepath.Walk(sp.path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors, continue walking
			}

			// Look for SKILL.md files (case-insensitive)
			if info.IsDir() {
				return nil
			}
			baseName := strings.ToLower(info.Name())
			if baseName != "skill.md" {
				return nil
			}

			skill, err := l.loadSkillFile(path, sp.scope, sp.namespace)
			if err != nil {
				return nil // Skip invalid skills
			}

			// Use FullName (namespace:name) as the key
			fullName := skill.FullName()

			// Higher priority scopes override lower ones
			if existing, ok := skills[fullName]; ok {
				if skill.Scope > existing.Scope {
					skills[fullName] = skill
				}
			} else {
				skills[fullName] = skill
			}

			return nil
		})
		if err != nil {
			continue // Skip directory errors
		}
	}

	return skills, nil
}

// loadSkillFile loads a skill from a file path.
// Only parses frontmatter for metadata; instructions are lazy-loaded.
func (l *loader) loadSkillFile(path string, scope SkillScope, defaultNamespace string) (*Skill, error) {
	fm, _, err := markdown.ParseFrontmatterFile(path)
	if err != nil {
		return nil, err
	}

	skillDir := filepath.Dir(path)

	skill := &Skill{
		FilePath: path,
		SkillDir: skillDir,
		Scope:    scope,
		State:    StateEnable,
	}

	if fm != "" {
		if err := yaml.Unmarshal([]byte(fm), skill); err != nil {
			return nil, err
		}
	}

	if skill.Name == "" {
		skill.Name = filepath.Base(skillDir)
	}

	if skill.Namespace == "" && defaultNamespace != "" {
		skill.Namespace = defaultNamespace
	}

	skill.Scripts = scanResourceDir(filepath.Join(skillDir, "scripts"))
	skill.References = scanResourceDir(filepath.Join(skillDir, "references"))
	skill.Assets = scanResourceDir(filepath.Join(skillDir, "assets"))

	return skill, nil
}

// scanResourceDir scans a directory and returns file names.
func scanResourceDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	return files
}

// loadInstructions loads the full instructions from a skill file.
func loadInstructions(path string) (string, error) {
	_, body, err := markdown.ParseFrontmatterFile(path)
	if err != nil {
		return "", err
	}
	return body, nil
}
