package selflearn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPatchTiers(t *testing.T) {
	const body = "line one\n    indented two\nthree  with   spaces\n"

	// Tier 1: exact.
	out, err := applyPatch(body, "line one", "line ONE", false)
	if err != nil || !strings.Contains(out, "line ONE") {
		t.Fatalf("exact: out=%q err=%v", out, err)
	}

	// Tier 2: line-trimmed (old has no leading whitespace, file does).
	out, err = applyPatch(body, "indented two", "indented TWO", false)
	if err != nil || !strings.Contains(out, "indented TWO") {
		t.Fatalf("line-trimmed: out=%q err=%v", out, err)
	}

	// Tier 3: whitespace-collapsed (old uses single spaces, file has runs).
	out, err = applyPatch(body, "three with spaces", "collapsed", false)
	if err != nil || !strings.Contains(out, "collapsed") {
		t.Fatalf("ws-collapsed: out=%q err=%v", out, err)
	}
}

func TestApplyPatchNotFound(t *testing.T) {
	if _, err := applyPatch("alpha\nbeta\n", "gamma", "x", false); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestApplyPatchAmbiguous(t *testing.T) {
	body := "dup\nmiddle\ndup\n"
	if _, err := applyPatch(body, "dup", "x", false); err == nil {
		t.Fatal("expected ambiguous error without replace_all")
	}
	out, err := applyPatch(body, "dup", "x", true)
	if err != nil || strings.Count(out, "x") != 2 {
		t.Fatalf("replace_all: out=%q err=%v", out, err)
	}
}

func TestApplyPatchEscapeDrift(t *testing.T) {
	body := "name = 'value'\n"
	// old_text carries a backslash-escaped quote the file never had.
	if _, err := applyPatch(body, `name = \'value\'`, "x", false); err == nil {
		t.Fatal("expected escape-drift rejection")
	}
}

// newTestSkillManager points user/project skill dirs at temp dirs.
func newTestSkillManager(t *testing.T) (*SkillManager, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd := t.TempDir()
	return NewSkillManager(cwd), cwd
}

func TestSkillCreateMarksAgentOrigin(t *testing.T) {
	mgr, _ := newTestSkillManager(t)
	if _, err := mgr.Create("go-table-tests", "table-driven test patterns", "Use t.Run subtests.", "user"); err != nil {
		t.Fatalf("create: %v", err)
	}
	path := filepath.Join(mgr.userDir, "go-table-tests", "SKILL.md")
	origin, err := readOrigin(path)
	if err != nil {
		t.Fatal(err)
	}
	if origin != agentOrigin {
		t.Fatalf("origin = %q, want %q", origin, agentOrigin)
	}
	// Duplicate create is rejected.
	if _, err := mgr.Create("go-table-tests", "", "x", "user"); err == nil {
		t.Fatal("duplicate create should error")
	}
}

func TestSkillCreateRejectsBadName(t *testing.T) {
	mgr, _ := newTestSkillManager(t)
	for _, bad := range []string{"Fix PR #123", "../escape", "has/slash", ""} {
		if _, err := mgr.Create(bad, "", "body", "user"); err == nil {
			t.Fatalf("name %q should be rejected", bad)
		}
	}
}

func TestSkillRefusesUserCreated(t *testing.T) {
	mgr, _ := newTestSkillManager(t)
	// Hand-write a user-created skill (no origin field).
	dir := filepath.Join(mgr.userDir, "hand-written")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: hand-written\ndescription: by a human\n---\n\nOriginal body.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Patch("hand-written", "Original", "Hacked", false); err == nil {
		t.Fatal("patch of user-created skill must be refused")
	}
	if _, err := mgr.Delete("hand-written"); err == nil {
		t.Fatal("delete of user-created skill must be refused")
	}
}

func TestSkillPatchAndEdit(t *testing.T) {
	mgr, _ := newTestSkillManager(t)
	if _, err := mgr.Create("go-errs", "error wrapping", "Wrap with %w always.", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Patch("go-errs", "Wrap with %w always.", "Wrap with %w; add context.", false); err != nil {
		t.Fatalf("patch: %v", err)
	}
	_, body, _ := parseSkill(t, mgr, "go-errs")
	if !strings.Contains(body, "add context") {
		t.Fatalf("patch not applied: %q", body)
	}
	// Edit preserves frontmatter (origin) while rewriting the body.
	if _, err := mgr.Edit("go-errs", "Completely new body."); err != nil {
		t.Fatalf("edit: %v", err)
	}
	origin, body, _ := parseSkill(t, mgr, "go-errs")
	if origin != agentOrigin {
		t.Fatalf("edit dropped origin: %q", origin)
	}
	if !strings.Contains(body, "Completely new body") {
		t.Fatalf("edit not applied: %q", body)
	}
}

func TestSkillSupportFiles(t *testing.T) {
	mgr, _ := newTestSkillManager(t)
	if _, err := mgr.Create("with-refs", "", "Body.", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.WriteFile("with-refs", "references/cheatsheet.md", "# Cheatsheet"); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	ref := filepath.Join(mgr.userDir, "with-refs", "references", "cheatsheet.md")
	if _, err := os.Stat(ref); err != nil {
		t.Fatalf("support file missing: %v", err)
	}
	// Traversal / wrong subdir rejected.
	if _, err := mgr.WriteFile("with-refs", "../evil.md", "x"); err == nil {
		t.Fatal("traversal support file should be rejected")
	}
	if _, err := mgr.WriteFile("with-refs", "secrets/x.md", "x"); err == nil {
		t.Fatal("non-whitelisted subdir should be rejected")
	}
	if _, err := mgr.RemoveFile("with-refs", "references/cheatsheet.md"); err != nil {
		t.Fatalf("remove_file: %v", err)
	}
	if _, err := os.Stat(ref); !os.IsNotExist(err) {
		t.Fatal("support file should be gone")
	}
}

func TestSkillProjectOverridesUser(t *testing.T) {
	mgr, _ := newTestSkillManager(t)
	// Same name at both scopes; resolve must prefer project.
	if _, err := mgr.Create("dual", "", "user body", "user"); err != nil {
		t.Fatal(err)
	}
	projDir := filepath.Join(mgr.projectDir, "dual")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := buildSkillMD("dual", "", agentOrigin, "project body")
	if err := os.WriteFile(filepath.Join(projDir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := mgr.resolve("dual")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, mgr.projectDir) {
		t.Fatalf("resolve preferred %q, want project scope", path)
	}
}

func TestSkillManageToolDispatch(t *testing.T) {
	mgr, _ := newTestSkillManager(t)
	tool := newSkillManageTool(mgr)
	out, err := tool.Execute(context.Background(), map[string]any{
		"action":  "create",
		"name":    "tool-made",
		"content": "Body from tool.",
		"level":   "user",
	})
	if err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("create via tool: out=%q err=%v", out, err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"action": "create"}); err == nil {
		t.Fatal("missing name should error")
	}
}

func parseSkill(t *testing.T, mgr *SkillManager, name string) (origin, body string, path string) {
	t.Helper()
	p, err := mgr.resolve(name)
	if err != nil {
		t.Fatal(err)
	}
	origin, err = readOrigin(p)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "---", 3)
	if len(parts) == 3 {
		body = strings.TrimSpace(parts[2])
	}
	return origin, body, p
}
