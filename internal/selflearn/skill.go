package selflearn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/markdown"
	"gopkg.in/yaml.v3"
)

// agentOrigin is the provenance value L1 writes; only skills carrying it are
// mutable by the reviewer (it reads user-created skills but never modifies
// them). See notes/active/l1-background-review.md §5.2.
const agentOrigin = "agent-created"

// skillNameRe enforces class-level kebab names and doubles as a traversal guard
// (no separators, no dots). Session-specific names (PR numbers, error strings)
// should be steered away by the prompt; this just keeps the on-disk name safe.
var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// supportSubdirs are the only places a skill_manage support file may be written.
var supportSubdirs = map[string]struct{}{"references": {}, "templates": {}, "scripts": {}}

// ActionPermissions controls what L1 may do via skill_manage. See
// notes/active/l1-background-review.md §5.5.
//
// The first three flags restrict actions only on agent-created skills.
// AllowUpdateUserCreated is the single advanced opt-in that extends
// AllowUpdate to also patch user-created skills — create and delete on
// user-created remain impossible at any setting.
type ActionPermissions struct {
	AllowCreate            bool
	AllowUpdate            bool
	AllowDelete            bool
	AllowUpdateUserCreated bool
}

// DefaultActionPermissions is the safe default: everything allowed within
// the agent-created scope; user-created stays read-only.
func DefaultActionPermissions() ActionPermissions {
	return ActionPermissions{AllowCreate: true, AllowUpdate: true, AllowDelete: true}
}

// SkillWriteObserver is invoked after every successful create / patch /
// edit / write_file / remove_file / delete. action is the §5.3 action name;
// name is the affected skill.
//
// Contract: SetWriteObserver MUST be called before the first write; the
// reviewer fork is single-flight per session (§6 invariant #8) so we do
// not guard the observer field with a lock.
type SkillWriteObserver func(action, name string)

// SkillManager is the L1-only skill write surface. Skills live directly in
// gen-code's existing user/project scopes — ~/.gen/skills/<name>/ and
// ./.gen/skills/<name>/ — distinguished by the origin frontmatter field, not a
// subdirectory.
type SkillManager struct {
	userDir    string
	projectDir string
	perms      ActionPermissions
	onWrite    SkillWriteObserver

	mu sync.Mutex
}

// NewSkillManager returns the manager for cwd with the given action
// permissions. The skill dirs are created lazily on first create.
func NewSkillManager(cwd string, perms ActionPermissions) *SkillManager {
	home, _ := os.UserHomeDir()
	return &SkillManager{
		userDir:    filepath.Join(home, ".gen", "skills"),
		projectDir: filepath.Join(cwd, ".gen", "skills"),
		perms:      perms,
	}
}

// Perms returns the current action permissions (read-only snapshot).
func (m *SkillManager) Perms() ActionPermissions { return m.perms }

// SetWriteObserver registers the callback fired after each successful
// write. Must be called before the first write (see type doc).
func (m *SkillManager) SetWriteObserver(fn SkillWriteObserver) { m.onWrite = fn }

func (m *SkillManager) fireWrite(action, name string) {
	if m.onWrite != nil {
		m.onWrite(action, name)
	}
}

// SkillInfo is a one-line summary of an existing skill, used to brief the
// reviewer so it prefers updating over creating and never re-derives a skill
// that already exists.
type SkillInfo struct {
	Name        string
	Level       string // user | project
	Origin      string // agent-created | user-created
	Description string
	Editable    bool // true only for agent-created skills
}

// Inventory lists existing skills across both scopes (project entries shadow
// user entries of the same name, matching loader precedence).
func (m *SkillManager) Inventory() []SkillInfo {
	seen := make(map[string]bool)
	var out []SkillInfo
	for _, scope := range []struct {
		dir   string
		level string
	}{{m.projectDir, "project"}, {m.userDir, "user"}} {
		entries, err := os.ReadDir(scope.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
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
				Editable:    origin == agentOrigin,
			})
		}
	}
	return out
}

func (m *SkillManager) dirFor(level string) (string, error) {
	switch strings.TrimSpace(level) {
	case "", "user":
		return m.userDir, nil
	case "project":
		return m.projectDir, nil
	default:
		return "", fmt.Errorf("invalid level %q; use user or project", level)
	}
}

// resolve finds an existing skill's SKILL.md by name, project scope first
// (higher priority), then user. Returns the path or an error if absent.
func (m *SkillManager) resolve(name string) (string, error) {
	for _, dir := range []string{m.projectDir, m.userDir} {
		p := filepath.Join(dir, name, "SKILL.md")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no skill named %q", name)
}

// parsed is the result of locating + parsing a skill's SKILL.md once. It
// is the return type of the two guards below so callers (Edit, Patch)
// don't have to re-parse the same file.
type parsed struct {
	path   string
	origin string
	fm     string
	body   string
}

// parseSkill resolves name and parses its SKILL.md frontmatter exactly
// once. Returns the path, the origin field (empty ⇒ user-created), the
// raw frontmatter block, and the body.
func (m *SkillManager) parseSkill(name string) (parsed, error) {
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

// requireAgentOwned parses name and confirms it is agent-created. Used for
// actions where user-created remains off-limits at every config setting
// (delete, edit, write_file, remove_file).
func (m *SkillManager) requireAgentOwned(name string) (parsed, error) {
	p, err := m.parseSkill(name)
	if err != nil {
		return parsed{}, err
	}
	if p.origin != agentOrigin {
		return parsed{}, fmt.Errorf("skill %q is user-created and must not be modified by the reviewer", name)
	}
	return p, nil
}

// requirePatchable parses name and returns it when L1 is allowed to patch
// the body in place. Agent-created skills are always patchable (subject to
// AllowUpdate); user-created skills are patchable only when
// AllowUpdateUserCreated is set (§5.5 advanced opt-in).
func (m *SkillManager) requirePatchable(name string) (parsed, error) {
	p, err := m.parseSkill(name)
	if err != nil {
		return parsed{}, err
	}
	switch p.origin {
	case agentOrigin:
		return p, nil
	case "", "user-created":
		if m.perms.AllowUpdateUserCreated {
			return p, nil
		}
		return parsed{}, fmt.Errorf(
			"skill %q is user-created; set selfLearn.skills.allowUpdateUserCreated=true to allow patching",
			name,
		)
	default:
		return parsed{}, fmt.Errorf("skill %q has unknown origin %q", name, p.origin)
	}
}

func (m *SkillManager) Create(name, description, body, level string) (string, error) {
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
	// Skill bodies and descriptions are loaded into a future system prompt, so
	// they carry the same stored-injection risk as memory entries.
	if err := scanContent(body); err != nil {
		return "", err
	}
	if err := scanForThreats(description); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.resolve(name); err == nil {
		return "", fmt.Errorf("skill %q already exists; use patch or edit", name)
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
	m.fireWrite("create", name)
	return fmt.Sprintf("Created skill %q.", name), nil
}

func (m *SkillManager) Edit(name, body string) (string, error) {
	if !m.perms.AllowUpdate {
		return "", errActionDenied("edit", "allowUpdate=false")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("skill content cannot be empty")
	}
	if err := scanContent(body); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Edit is a full-body rewrite — restricted to agent-created skills even
	// with allowUpdateUserCreated=true. Hermes-style "patch a user skill" is
	// targeted; rewriting the whole body of someone's authored file goes too
	// far.
	p, err := m.requireAgentOwned(name)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p.path, []byte(joinFrontmatter(p.fm, body)), 0o644); err != nil {
		return "", err
	}
	m.fireWrite("edit", name)
	return fmt.Sprintf("Rewrote skill %q.", name), nil
}

func (m *SkillManager) Patch(name, oldText, newText string, replaceAll bool) (string, error) {
	if !m.perms.AllowUpdate {
		return "", errActionDenied("patch", "allowUpdate=false")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Patch is the one action whose target scope is extended by
	// allowUpdateUserCreated (§5.5). Other update-shaped actions (edit,
	// write_file, remove_file, delete) stay agent-created only.
	p, err := m.requirePatchable(name)
	if err != nil {
		return "", err
	}
	patched, err := applyPatch(p.body, oldText, newText, replaceAll)
	if err != nil {
		return "", err
	}
	// Scan the merged body so an injection assembled across patches is caught,
	// while still allowing a patch that legitimately removes text.
	if err := scanForThreats(patched); err != nil {
		return "", err
	}
	if err := os.WriteFile(p.path, []byte(joinFrontmatter(p.fm, patched)), 0o644); err != nil {
		return "", err
	}
	m.fireWrite("patch", name)
	return fmt.Sprintf("Patched skill %q.", name), nil
}

func (m *SkillManager) WriteFile(name, file, content string) (string, error) {
	if !m.perms.AllowUpdate {
		return "", errActionDenied("write_file", "allowUpdate=false")
	}
	// Support files (references/templates/scripts) are read or executed by the
	// agent, so they get the same threat scan as skill bodies.
	if err := scanForThreats(content); err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Support-file writes don't extend to user-created skills — adding
	// references/scripts to someone's authored skill is a structural change,
	// not a targeted patch, so it stays out of allowUpdateUserCreated.
	p, err := m.requireAgentOwned(name)
	if err != nil {
		return "", err
	}
	rel, err := safeSupportFile(file)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(filepath.Dir(p.path), rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		return "", err
	}
	m.fireWrite("write_file", name)
	return fmt.Sprintf("Wrote %s to skill %q.", rel, name), nil
}

func (m *SkillManager) RemoveFile(name, file string) (string, error) {
	if !m.perms.AllowUpdate {
		return "", errActionDenied("remove_file", "allowUpdate=false")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Same scope as WriteFile — agent-created only.
	p, err := m.requireAgentOwned(name)
	if err != nil {
		return "", err
	}
	rel, err := safeSupportFile(file)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(filepath.Dir(p.path), rel)
	if err := os.Remove(dest); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no such support file %q", rel)
		}
		return "", err
	}
	m.fireWrite("remove_file", name)
	return fmt.Sprintf("Removed %s from skill %q.", rel, name), nil
}

func (m *SkillManager) Delete(name string) (string, error) {
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
	if err := os.RemoveAll(filepath.Dir(p.path)); err != nil {
		return "", err
	}
	m.fireWrite("delete", name)
	return fmt.Sprintf("Deleted skill %q.", name), nil
}

// errActionDenied builds a uniform "permission denied" error for actions the
// configured ActionPermissions reject. Used as the early-return in the four
// action entry points so the model sees a consistent shape on the
// permission-veto path (§5.5).
func errActionDenied(action, reason string) error {
	return fmt.Errorf("skill_manage(%s) denied: %s (see selfLearn.skills permissions in §5.5)", action, reason)
}

// safeSupportFile validates a support-file path: <subdir>/<file>, where subdir
// is references/templates/scripts and file is a bare name.
func safeSupportFile(file string) (string, error) {
	file = strings.TrimSpace(strings.TrimPrefix(file, "./"))
	if file == "" || strings.Contains(file, "..") {
		return "", fmt.Errorf("invalid support file %q", file)
	}
	parts := strings.Split(filepath.ToSlash(file), "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("support file must be <references|templates|scripts>/<name>, got %q", file)
	}
	if _, ok := supportSubdirs[parts[0]]; !ok {
		return "", fmt.Errorf("support subdir must be references, templates, or scripts; got %q", parts[0])
	}
	if parts[1] != filepath.Base(parts[1]) || parts[1] == "" {
		return "", fmt.Errorf("invalid support file name %q", parts[1])
	}
	return filepath.Join(parts[0], parts[1]), nil
}

func readOrigin(path string) (string, error) {
	fm, _, err := markdown.ParseFrontmatterFile(path)
	if err != nil {
		return "", err
	}
	var meta struct {
		Origin string `yaml:"origin"`
	}
	if fm != "" {
		if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
			return "", err
		}
	}
	return meta.Origin, nil
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
// ParseFrontmatterFile, newline-terminated per line) to a new body.
func joinFrontmatter(fm, body string) string {
	fm = strings.TrimRight(fm, "\n")
	return "---\n" + fm + "\n---\n\n" + strings.TrimSpace(body) + "\n"
}

// yamlScalar quotes a description if it contains characters that would break a
// bare YAML scalar.
func yamlScalar(s string) string {
	if strings.ContainsAny(s, ":#\n\"'") {
		return strconv.Quote(s)
	}
	return s
}

// skillManageTool is the L1-only skill write surface.
type skillManageTool struct {
	mgr *SkillManager
}

func newSkillManageTool(mgr *SkillManager) *skillManageTool {
	return &skillManageTool{mgr: mgr}
}

func (t *skillManageTool) Name() string { return "skill_manage" }

func (t *skillManageTool) Description() string {
	return "Create or maintain an agent-created skill (a reusable, class-level technique). " +
		"Prefer updating an existing skill over creating a new one. Actions: " +
		"create (new class-level skill), patch (targeted find-and-replace), edit (full body rewrite — rare), " +
		"write_file/remove_file (references|templates|scripts support files), delete. " +
		"Only skills with origin: agent-created may be modified."
}

func (t *skillManageTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"create", "patch", "edit", "write_file", "remove_file", "delete"},
				},
				"name":        map[string]any{"type": "string", "description": "Class-level kebab-case skill name."},
				"description": map[string]any{"type": "string", "description": "One-line skill description (create)."},
				"content":     map[string]any{"type": "string", "description": "Body for create/edit, or support-file content for write_file."},
				"level":       map[string]any{"type": "string", "enum": []string{"user", "project"}, "description": "Scope for create (default user)."},
				"old_text":    map[string]any{"type": "string", "description": "Text to find (patch)."},
				"new_text":    map[string]any{"type": "string", "description": "Replacement text (patch)."},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace every match (patch)."},
				"file":        map[string]any{"type": "string", "description": "Support file as <references|templates|scripts>/<name>."},
			},
			"required": []string{"action", "name"},
		},
	}
}

func (t *skillManageTool) Execute(_ context.Context, in map[string]any) (string, error) {
	action := strings.TrimSpace(str(in["action"]))
	name := strings.TrimSpace(str(in["name"]))
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	var (
		msg string
		err error
	)
	switch action {
	case "create":
		msg, err = t.mgr.Create(name, str(in["description"]), str(in["content"]), str(in["level"]))
	case "patch":
		msg, err = t.mgr.Patch(name, str(in["old_text"]), str(in["new_text"]), boolOf(in["replace_all"]))
	case "edit":
		msg, err = t.mgr.Edit(name, str(in["content"]))
	case "write_file":
		msg, err = t.mgr.WriteFile(name, str(in["file"]), str(in["content"]))
	case "remove_file":
		msg, err = t.mgr.RemoveFile(name, str(in["file"]))
	case "delete":
		msg, err = t.mgr.Delete(name)
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]string{"status": "ok", "message": msg})
	return string(out), nil
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}
