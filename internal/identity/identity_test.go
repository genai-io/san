package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func writeIdentityFile(t *testing.T, dir, filename, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestParseFile_UsesFrontmatterFields(t *testing.T) {
	dir := t.TempDir()
	path := writeIdentityFile(t, dir, "ml-engineer.md", `---
name: ml-engineer
description: ML engineering specialist
---

You are an ML engineer.

# Tone
Be concise.
`)

	id, err := parseFile(path)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if id.Name != "ml-engineer" {
		t.Fatalf("Name = %q, want %q", id.Name, "ml-engineer")
	}
	if id.Description != "ML engineering specialist" {
		t.Fatalf("Description = %q", id.Description)
	}
	if id.Body == "" {
		t.Fatal("Body should not be empty")
	}
}

func TestParseFile_TrimsFrontmatterFields(t *testing.T) {
	dir := t.TempDir()
	path := writeIdentityFile(t, dir, "ml-engineer.md", `---
name: " ml-engineer "
description: " ML engineering specialist "
---

body
`)

	id, err := parseFile(path)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if id.Name != "ml-engineer" {
		t.Fatalf("Name = %q, want trimmed name", id.Name)
	}
	if id.Description != "ML engineering specialist" {
		t.Fatalf("Description = %q, want trimmed description", id.Description)
	}
}

func TestParseFile_FallsBackToFilenameStem(t *testing.T) {
	dir := t.TempDir()
	path := writeIdentityFile(t, dir, "rust-systems.md", `---
description: Rust systems persona
---

You are a Rust systems engineer.
`)

	id, err := parseFile(path)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if id.Name != "rust-systems" {
		t.Fatalf("Name = %q, want filename stem %q", id.Name, "rust-systems")
	}
}

func TestParseFile_HandlesBrokenFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := writeIdentityFile(t, dir, "broken.md", `---
name: : :
this isn't valid yaml
---

body
`)

	id, err := parseFile(path)
	if err != nil {
		t.Fatalf("parseFile should tolerate broken yaml, got: %v", err)
	}
	if id.Name == "" {
		t.Fatal("Name should fall back to filename stem when yaml fails")
	}
}

func TestSortIdentities_DefaultProjectUser(t *testing.T) {
	items := []*Identity{
		{Name: "z-user", Scope: ScopeUser},
		{Name: "a-project", Scope: ScopeProject},
		{Name: "default", Scope: ScopeBuiltin},
		{Name: "a-user", Scope: ScopeUser},
		{Name: "b-project", Scope: ScopeProject},
	}
	sortIdentities(items)

	wantOrder := []string{"default", "a-project", "b-project", "a-user", "z-user"}
	for i, want := range wantOrder {
		if items[i].Name != want {
			t.Fatalf("position %d: got %q, want %q (full order: %v)",
				i, items[i].Name, want, names(items))
		}
	}
}

func names(items []*Identity) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

func TestActive_EmptyAndDefaultReturnNothing(t *testing.T) {
	r := &Registry{
		byName:     map[string]*Identity{"default": DefaultIdentity()},
		identities: []*Identity{DefaultIdentity()},
	}
	if got := r.Active(""); got != "" {
		t.Errorf(`Active("") = %q, want ""`, got)
	}
	if got := r.Active("default"); got != "" {
		t.Errorf(`Active("default") = %q, want ""`, got)
	}
}

func TestActive_UnknownNameReturnsEmpty(t *testing.T) {
	r := &Registry{
		byName:     map[string]*Identity{"default": DefaultIdentity()},
		identities: []*Identity{DefaultIdentity()},
	}
	if got := r.Active("nonexistent"); got != "" {
		t.Errorf("Active(unknown) = %q, want \"\"", got)
	}
}

func TestActive_ReturnsBodyForKnownIdentity(t *testing.T) {
	id := &Identity{Name: "ml-engineer", Body: "ML engineer body", Scope: ScopeUser}
	r := &Registry{
		byName:     map[string]*Identity{"default": DefaultIdentity(), "ml-engineer": id},
		identities: []*Identity{DefaultIdentity(), id},
	}
	if got := r.Active("ml-engineer"); got != "ML engineer body" {
		t.Errorf("Active = %q, want %q", got, "ML engineer body")
	}
}

func TestRegistry_ProjectOverridesUser(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeIdentityFile(t, filepath.Join(home, ".san", "identities"), "ml.md",
		"---\nname: ml\ndescription: user-level ml\n---\n\nuser body\n")
	writeIdentityFile(t, filepath.Join(cwd, ".san", "identities"), "ml.md",
		"---\nname: ml\ndescription: project-level ml\n---\n\nproject body\n")

	r := NewRegistry(cwd)
	got, ok := r.Get("ml")
	if !ok {
		t.Fatal("expected ml identity to be registered")
	}
	if got.Scope != ScopeProject {
		t.Errorf("scope = %v, want ScopeProject", got.Scope)
	}
	if got.Description != "project-level ml" {
		t.Errorf("description = %q, want project-level", got.Description)
	}

	if body := r.Active("ml"); body != "project body" {
		t.Errorf("Active body = %q, want project body", body)
	}
}

func TestLoadDir_SkipsReadme(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "README.md", "not an identity")
	writeIdentityFile(t, dir, "real.md",
		"---\nname: real\ndescription: real one\n---\n\nbody\n")

	got := loadDir(dir, ScopeUser)
	if len(got) != 1 {
		t.Fatalf("expected 1 identity (README skipped), got %d", len(got))
	}
	if got[0].Name != "real" {
		t.Errorf("Name = %q, want real", got[0].Name)
	}
}

func TestLoadDir_SkipsReservedDefaultIdentity(t *testing.T) {
	dir := t.TempDir()
	writeIdentityFile(t, dir, "default.md",
		"---\nname: default\ndescription: custom default\n---\n\nbody\n")
	writeIdentityFile(t, dir, "real.md",
		"---\nname: real\ndescription: real one\n---\n\nbody\n")

	got := loadDir(dir, ScopeUser)
	if len(got) != 1 {
		t.Fatalf("expected only non-default identity, got %d", len(got))
	}
	if got[0].Name != "real" {
		t.Errorf("Name = %q, want real", got[0].Name)
	}
}

func TestIsIdentityFile(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	userIdentity := filepath.Join(home, ".san", "identities", "ml.md")
	projectIdentity := filepath.Join(cwd, ".san", "identities", "go.md")

	for _, path := range []string{userIdentity, projectIdentity} {
		if !IsIdentityFile(cwd, path) {
			t.Fatalf("IsIdentityFile(%q) = false, want true", path)
		}
	}

	for _, path := range []string{
		filepath.Join(home, ".san", "identities", "README.md"),
		filepath.Join(home, ".san", "other", "ml.md"),
		filepath.Join(cwd, ".san", "identities", "notes.txt"),
		filepath.Join(cwd, "identity.md"),
	} {
		if IsIdentityFile(cwd, path) {
			t.Fatalf("IsIdentityFile(%q) = true, want false", path)
		}
	}
}

func TestEnsureUserDir_IsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := EnsureUserDir(); err != nil {
		t.Fatalf("EnsureUserDir first call: %v", err)
	}

	readme := filepath.Join(home, ".san", "identities", "README.md")
	first, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Mutate the README, then call again — must not be overwritten.
	custom := []byte("user-edited README\n")
	if err := os.WriteFile(readme, custom, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := EnsureUserDir(); err != nil {
		t.Fatalf("EnsureUserDir second call: %v", err)
	}

	got, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("ReadFile after second call: %v", err)
	}
	if string(got) != string(custom) {
		t.Errorf("README was overwritten — got %q, want %q (initial template = %d bytes)",
			got, custom, len(first))
	}
}

func TestDefaultIdentity_IsBuiltin(t *testing.T) {
	id := DefaultIdentity()
	if !id.IsBuiltin() {
		t.Error("DefaultIdentity should report IsBuiltin() == true")
	}
	if id.Body != "" {
		t.Errorf("DefaultIdentity.Body should be empty, got %q", id.Body)
	}
}
