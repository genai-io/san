// Package skill provides skill management for GenCode.
// Skills are markdown-based prompts that can be invoked via slash commands
// or proactively by the model when active.
package skill

// SkillState represents the state of a skill.
// Three states control visibility and model awareness.
type SkillState string

const (
	// StateDisable means the skill is hidden and not available.
	StateDisable SkillState = "disable"

	// StateEnable means the skill is visible as a slash command but the model
	// is not aware of it (user-invoked only).
	StateEnable SkillState = "enable"

	// StateActive means the skill metadata is included in the system prompt
	// and the model can invoke it proactively.
	StateActive SkillState = "active"
)

// NextState cycles through states: disable -> enable -> active -> disable.
func (s SkillState) NextState() SkillState {
	switch s {
	case StateDisable:
		return StateEnable
	case StateEnable:
		return StateActive
	case StateActive:
		return StateDisable
	default:
		return StateEnable
	}
}

// SkillScope represents where a skill was loaded from.
// Higher values have higher priority.
type SkillScope int

const (
	// ScopeClaudeUser is ~/.claude/skills/ (lowest priority, Claude compatibility)
	ScopeClaudeUser SkillScope = iota

	// ScopeUserPlugin is ~/.gen/plugins/*/skills/ (User plugins)
	ScopeUserPlugin

	// ScopeUser is ~/.gen/skills/ (GenCode user level)
	ScopeUser

	// ScopeClaudeProject is .claude/skills/ (Claude project compatibility)
	ScopeClaudeProject

	// ScopeProjectPlugin is .gen/plugins/*/skills/ (Project plugins)
	ScopeProjectPlugin

	// ScopeProject is .gen/skills/ (GenCode project level, highest priority)
	ScopeProject
)

// String returns the display name for the scope.
func (s SkillScope) String() string {
	switch s {
	case ScopeClaudeUser:
		return "claude-user"
	case ScopeUserPlugin:
		return "user-plugin"
	case ScopeUser:
		return "user"
	case ScopeClaudeProject:
		return "claude-project"
	case ScopeProjectPlugin:
		return "project-plugin"
	case ScopeProject:
		return "project"
	default:
		return "unknown"
	}
}

// Skill represents a loaded skill with metadata and instructions.
type Skill struct {
	// Frontmatter fields (parsed from YAML header)
	Name         string   `yaml:"name"`
	Namespace    string   `yaml:"namespace"` // Optional namespace (e.g., "git", "jira")
	Description  string   `yaml:"description"`
	AllowedTools []string `yaml:"allowed-tools"`
	ArgumentHint string   `yaml:"argument-hint"`
	// Origin marks provenance. Absent/empty means "user-created" (the
	// default); other producers may set a non-empty value to mark the
	// source so downstream consumers can scope writes by provenance.
	Origin string `yaml:"origin,omitempty"`

	// Runtime fields
	FilePath string     // Full path to the skill file
	SkillDir string     // Directory containing the skill
	Scope    SkillScope // Where the skill was loaded from
	State    SkillState // Current state (persisted separately)

	// Resource directories (Agent Skills spec)
	Scripts    []string // Files in scripts/ directory
	References []string // Files in references/ directory
	Assets     []string // Files in assets/ directory
}

// FullName returns the namespaced skill name (namespace:name or just name).
func (s *Skill) FullName() string {
	if s.Namespace != "" {
		return s.Namespace + ":" + s.Name
	}
	return s.Name
}

// IsEnabled returns true if the skill is enabled or active.
func (s *Skill) IsEnabled() bool {
	return s.State == StateEnable || s.State == StateActive
}

// IsActive returns true if the skill is active (model aware).
func (s *Skill) IsActive() bool {
	return s.State == StateActive
}

// GetInstructions returns the full skill instructions, reading from disk each time.
// This ensures modifications to SKILL.md files are immediately reflected.
func (s *Skill) GetInstructions() string {
	if s.FilePath == "" {
		return ""
	}
	instructions, _ := loadInstructions(s.FilePath)
	return instructions
}

// HasResources returns true if the skill has any bundled resources.
func (s *Skill) HasResources() bool {
	return len(s.Scripts) > 0 || len(s.References) > 0 || len(s.Assets) > 0
}
