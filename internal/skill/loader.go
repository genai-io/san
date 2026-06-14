package skill

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/markdown"

	"gopkg.in/yaml.v3"
)

// Loader handles loading skills from multiple directories.
type loader struct {
	cwd string // Current working directory for project-level skills
	// pluginSkillPaths, when set, supplies the skill directories contributed
	// by enabled plugins (injected by the app from the plugin registry). nil
	// in contexts that don't load plugin skills (e.g. persona, tests).
	pluginSkillPaths func() []PluginSkillPath
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
// Plugin skills are injected by the app from the plugin registry (only enabled
// plugins), keeping discovery consistent with commands and subagents instead of
// re-parsing installed_plugins.json here.
func (l *loader) getSearchPaths() []searchPath {
	homeDir, _ := os.UserHomeDir()

	userPlugins, projectPlugins := l.pluginPaths()

	var paths []searchPath

	// 1. ~/.claude/skills/ (Claude user compat - lowest priority)
	paths = append(paths, searchPath{
		path:  filepath.Join(homeDir, ".claude", "skills"),
		scope: ScopeClaudeUser,
	})

	// 2. User-scope plugin skills
	paths = append(paths, userPlugins...)

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

	// 5. Project-scope plugin skills
	paths = append(paths, projectPlugins...)

	// 6. .san/skills/ (Project level - highest priority)
	paths = append(paths, searchPath{
		path:  filepath.Join(confdir.Dir(l.cwd), "skills"),
		scope: ScopeProject,
	})

	return paths
}

// pluginPaths splits the injected plugin skill directories into user- and
// project-scope buckets. Each directory contains a SKILL.md and inherits the
// plugin name as its default namespace. Returns nil/nil when no callback is set.
func (l *loader) pluginPaths() (user, project []searchPath) {
	if l.pluginSkillPaths == nil {
		return nil, nil
	}
	for _, p := range l.pluginSkillPaths() {
		if p.IsProject {
			project = append(project, searchPath{path: p.Path, scope: ScopeProjectPlugin, namespace: p.Namespace})
		} else {
			user = append(user, searchPath{path: p.Path, scope: ScopeUserPlugin, namespace: p.Namespace})
		}
	}
	return user, project
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
