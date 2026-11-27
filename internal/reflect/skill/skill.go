package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/genai-io/san/internal/infra/confdir"
	"github.com/genai-io/san/internal/infra/markdown"
	"gopkg.in/yaml.v3"
)

// agentOrigin marks skills that Auto Skill may modify.
const agentOrigin = "agent-created"

// skillNameRe enforces class-level kebab names and doubles as a traversal guard
// (no separators, no dots). Session-specific names (PR numbers, error strings)
// should be steered away by the prompt; this just keeps the on-disk name safe.
var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// SkillPermissions bounds writes to agent-owned skills.
type SkillPermissions struct {
	AllowCreate bool
	AllowUpdate bool
	AllowDelete bool
}

// Any reports whether at least one action is allowed.
func (p SkillPermissions) Any() bool { return p.AllowCreate || p.AllowUpdate || p.AllowDelete }

// AllowAllSkillActions is the permissive set: every action allowed within
// the agent-created scope; user-created stays read-only regardless.
func AllowAllSkillActions() SkillPermissions {
	return SkillPermissions{AllowCreate: true, AllowUpdate: true, AllowDelete: true}
}

// Editor is the L1-only skill write surface. Skills live directly in
// san's existing user/project scopes — ~/.san/skills/<name>/ and
// ./.san/skills/<name>/ — distinguished by the origin frontmatter field, not a
// subdirectory.
type Editor struct {
	userDir    string
	projectDir string
	perms      SkillPermissions

	mu sync.Mutex
}

// NewEditor returns the editor for cwd with the given action
// permissions. The skill dirs are created lazily on first create. When
// the user home directory is unavailable (sandboxed exec, unset HOME),
// userDir is left empty and any user-scope write fails loudly via
// dirFor — better than silently aliasing user-scope onto cwd/.san/skills.
func NewEditor(cwd string, perms SkillPermissions) *Editor {
	userDir := ""
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		userDir = filepath.Join(confdir.Dir(home), "skills")
	}
	return &Editor{
		userDir:    userDir,
		projectDir: filepath.Join(confdir.Dir(cwd), "skills"),
		perms:      perms,
	}
}

// Perms returns the current action permissions (read-only snapshot).
func (m *Editor) Perms() SkillPermissions { return m.perms }

// SkillInfo is a one-line summary of an existing skill, used to brief the
// reviewer so it prefers updating over creating and never re-derives a skill
// that already exists.
type SkillInfo struct {
	Name        string
	Level       string // user | project
	Origin      string // agent-created | user-created
	Description string
}

// Editable reports whether the reviewer may modify this skill (agent-created).
// User-created skills are read-only to the reviewer at every setting.
func (i SkillInfo) Editable() bool { return i.Origin == agentOrigin }

// Inventory lists existing skills across both scopes (project entries shadow
// user entries of the same name, matching loader precedence).
//
// Why the disk scan instead of consulting skill.Registry: the registry is
// initialized once at startup and only refreshed on
// ReloadAfterPluginChange. The L1 reviewer mutates ~/.san/skills and
// ./.san/skills mid-session via skill_manage; a registry-backed Inventory
// would not see skills the reviewer just created in earlier passes, which
// would cause it to re-create duplicates. Reading the on-disk state
// directly is the correct freshness for this caller.
func (m *Editor) Inventory() []SkillInfo {
	seen := make(map[string]bool)
	var out []SkillInfo
	for _, scope := range []struct {
		dir   string
		level string
	}{{m.projectDir, "project"}, {m.userDir, "user"}} {
		if scope.dir == "" {
			continue
		}
		entries, err := os.ReadDir(scope.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !confdir.ResolvesToDir(scope.dir, e) || seen[e.Name()] {
				continue
			}
			path := filepath.Join(scope.dir, e.Name(), "SKILL.md")
			fm, _, err := markdown.ParseFrontmatterFile(path)
			if err != nil {
				continue
			}
			var meta struct {
				Description string `yaml:"description"`
				Origin      string `yaml:"origin"`
			}
			_ = yaml.Unmarshal([]byte(fm), &meta)
			origin := meta.Origin
			if origin == "" {
				origin = "user-created"
			}
			seen[e.Name()] = true
			out = append(out, SkillInfo{
				Name:        e.Name(),
				Level:       scope.level,
				Origin:      origin,
				Description: meta.Description,
			})
		}
	}
	return out
}

func (m *Editor) dirFor(level string) (string, error) {
	switch strings.TrimSpace(level) {
	case "", "user":
		if m.userDir == "" {
			return "", fmt.Errorf("user home directory unavailable; user-scope skills cannot be written")
		}
		return m.userDir, nil
	case "project":
		return m.projectDir, nil
	default:
		return "", fmt.Errorf("invalid level %q; use user or project", level)
	}
}

// resolve finds an existing skill's SKILL.md by name, project scope first
// (higher priority), then user. Returns the path or an error if absent.
func (m *Editor) resolve(name string) (string, error) {
	// Validate names before using them as directory components.
	if !skillNameRe.MatchString(name) {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	for _, dir := range []string{m.projectDir, m.userDir} {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name, "SKILL.md")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no skill named %q", name)
}

// Read returns the full SKILL.md content for preview, project scope first.
func (m *Editor) Read(name string) (string, error) {
	path, err := m.resolve(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parsed is the on-disk revision used to check that a reviewed skill hasn't changed.
type parsed struct {
	path   string
	origin string
	fm     string
	body   string
}

// parseSkill resolves name and parses its SKILL.md frontmatter exactly
// once. Returns the path, the origin field (empty ⇒ user-created), the
// raw frontmatter block, and the body.
func (m *Editor) parseSkill(name string) (parsed, error) {
	path, err := m.resolve(name)
	if err != nil {
		return parsed{}, err
	}
	fm, body, err := markdown.ParseFrontmatterFile(path)
	if err != nil {
		return parsed{}, err
	}
	var meta struct {
		Origin string `yaml:"origin"`
	}
	if fm != "" {
		if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
			return parsed{}, err
		}
	}
	return parsed{path: path, origin: meta.Origin, fm: fm, body: body}, nil
}

// requireAgentOwned loads a skill and checks that Auto Skill may modify it.
func (m *Editor) requireAgentOwned(name string) (parsed, error) {
	p, err := m.parseSkill(name)
	if err != nil {
		return parsed{}, err
	}
	if p.origin != agentOrigin {
		return parsed{}, fmt.Errorf("skill %q is user-created and must not be modified by the reviewer", name)
	}
	return p, nil
}

func (m *Editor) Create(name, description, body, level string) (string, error) {
	if !m.perms.AllowCreate {
		return "", errActionDenied("create", "allowCreate=false")
	}
	if !skillNameRe.MatchString(name) {
		return "", fmt.Errorf("invalid skill name %q; use a class-level kebab-case name (e.g. go-table-tests)", name)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("skill content cannot be empty")
	}
	// Description is the line Inventory exposes to future reviewers for
	// UPDATE/CREATE disambiguation (§5.1). An empty description silently
	// degrades the "prefer update over create" policy to substring-on-name.
	description = strings.TrimSpace(description)
	if description == "" {
		return "", fmt.Errorf("skill description cannot be empty")
	}
	// Skill bodies and descriptions are loaded into a future system prompt, so
	// they carry a stored-injection risk.
	if err := scanContent(body); err != nil {
		return "", err
	}
	if err := scanForThreats(description); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.resolve(name); err == nil {
		return "", fmt.Errorf("skill %q already exists; use patch", name)
	}
	dir, err := m.dirFor(level)
	if err != nil {
		return "", err
	}
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", err
	}
	content := buildSkillMD(name, description, agentOrigin, body)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Created skill %q.", name), nil
}

func (m *Editor) patch(name, oldText, newText string, expected parsed) (string, error) {
	if !m.perms.AllowUpdate {
		return "", errActionDenied("patch", "allowUpdate=false")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Every skill_manage action is agent-created only — the reviewer never
	// modifies user-authored skills.
	p, err := m.requireAgentOwned(name)
	if err != nil {
		return "", err
	}
	if p != expected {
		return "", fmt.Errorf("skill %q changed during Auto Skill review", name)
	}
	patched, err := applyPatch(p.body, oldText, newText, false)
	if err != nil {
		return "", err
	}
	// Only block patches that INTRODUCE a new threat pattern; preserving a
	// pattern already in the original (a legitimate injection-defense
	// example, a quoted attacker payload, etc.) must not brick the skill.
	// scanForThreats(patched) would have refused every later edit of such
	// a skill, including ones removing the very text that tripped the scan.
	if err := scanNewThreats(p.body, patched); err != nil {
		return "", err
	}
	if err := os.WriteFile(p.path, []byte(joinFrontmatter(p.fm, patched)), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Patched skill %q.", name), nil
}

func (m *Editor) Delete(name string) (string, error) {
	return m.delete(name, nil)
}

func (m *Editor) delete(name string, expected *parsed) (string, error) {
	if !m.perms.AllowDelete {
		return "", errActionDenied("delete", "allowDelete=false")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Delete is always restricted to agent-created — no config setting
	// (including allowUpdateUserCreated) opens user-created deletion (§5.5).
	p, err := m.requireAgentOwned(name)
	if err != nil {
		return "", err
	}
	if expected != nil && p != *expected {
		return "", fmt.Errorf("skill %q changed during Auto Skill review", name)
	}
	if err := os.RemoveAll(filepath.Dir(p.path)); err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted skill %q.", name), nil
}

// errActionDenied builds a uniform "permission denied" error for actions the
// configured SkillPermissions reject. Used as the early-return in the four
// action entry points so the model sees a consistent shape on the
// permission-veto path (§5.5).
func errActionDenied(action, reason string) error {
	return fmt.Errorf("skill_manage(%s) denied: %s (see selfLearn.skills permissions in §5.5)", action, reason)
}

func buildSkillMD(name, description, origin, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + name + "\n")
	if description != "" {
		b.WriteString("description: " + yamlScalar(description) + "\n")
	}
	b.WriteString("origin: " + origin + "\n")
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String()
}

// joinFrontmatter reattaches existing frontmatter (as returned by
// ParseFrontmatterFile, newline-terminated per line) to a new body. When
// the input had no frontmatter at all (fm empty), the body is returned
// alone — fabricating an empty `---\n\n---\n\n` block here would silently
// mutate a user-authored SKILL.md into a different on-disk shape.
func joinFrontmatter(fm, body string) string {
	fm = strings.TrimRight(fm, "\n")
	if fm == "" {
		return strings.TrimSpace(body) + "\n"
	}
	return "---\n" + fm + "\n---\n\n" + strings.TrimSpace(body) + "\n"
}

// yamlScalar renders a description as a YAML scalar that always round-trips.
// A hand-rolled "quote only these chars" rule under-quotes values that open a
// YAML indicator (e.g. "fix [bug"), producing frontmatter that parses as a
// flow sequence — or fails to parse at all, which would make every later
// parseSkill on that file error and leave the skill permanently un-editable.
// Delegating to the YAML encoder guarantees a valid scalar for any input.
func yamlScalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return strconv.Quote(s)
	}
	scalar := strings.TrimRight(string(out), "\n")
	// A multi-line value marshals to a block scalar, which cannot be inlined
	// after "description: ". Fall back to a double-quoted single line (valid
	// YAML, \n-escaped) for that case.
	if strings.Contains(scalar, "\n") {
		return strconv.Quote(s)
	}
	return scalar
}
