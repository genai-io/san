package persona

import (
	"path/filepath"
	"testing"
)

func TestRegistry_AbsorbsLegacyIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".san", "identities", "ml-engineer.md"),
		"---\nname: ml-engineer\ndescription: ML specialist\n---\n\nYou are an ML engineer.\n")

	r := NewRegistry("")
	p, ok := r.Get("ml-engineer")
	if !ok {
		t.Fatal("a legacy identity file should load as a persona")
	}
	if p.Identity != "You are an ML engineer." {
		t.Errorf("Identity = %q", p.Identity)
	}
	if p.Description != "ML specialist" {
		t.Errorf("Description = %q", p.Description)
	}
	if len(p.SkillDirs) != 0 || p.Settings != nil {
		t.Error("an absorbed identity should have no skills and no settings overlay")
	}
}

func TestRegistry_LegacyIdentityFallsBackToFilename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".san", "identities", "rust-systems.md"),
		"---\ndescription: Rust systems\n---\nYou are a Rust engineer.\n")

	r := NewRegistry("")
	if _, ok := r.Get("rust-systems"); !ok {
		t.Error("name should fall back to the filename stem when frontmatter omits it")
	}
}

func TestRegistry_PersonaDirBeatsIdentityFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Same name from both sources: a real persona dir and a legacy identity.
	writeFile(t, filepath.Join(home, ".san", "personas", "foo", "system", "identity.md"), "persona-dir version")
	writeFile(t, filepath.Join(home, ".san", "identities", "foo.md"), "---\nname: foo\n---\nidentity-file version")

	r := NewRegistry("")
	p, ok := r.Get("foo")
	if !ok {
		t.Fatal("foo should resolve")
	}
	if p.Identity != "persona-dir version" {
		t.Errorf("Identity = %q, want the persona-dir version to win over the identity file", p.Identity)
	}
}

func TestRegistry_LegacyIdentitySkipsDefaultAndReadme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".san", "identities", "README.md"), "docs, not an identity")
	writeFile(t, filepath.Join(home, ".san", "identities", "default.md"), "---\nname: default\n---\nshould be skipped")
	writeFile(t, filepath.Join(home, ".san", "identities", "real.md"), "---\nname: real\n---\nreal one")

	r := NewRegistry("")
	if _, ok := r.Get("real"); !ok {
		t.Error("a real identity should load")
	}
	if got, _ := r.Get("default"); got == nil || !got.IsBuiltin() {
		t.Error("'default' must remain the virtual builtin, not a loaded identity file")
	}
}

func TestRegistry_ProjectIdentityOverridesUserIdentity(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".san", "identities", "ml.md"), "---\nname: ml\n---\nuser body")
	writeFile(t, filepath.Join(cwd, ".san", "identities", "ml.md"), "---\nname: ml\n---\nproject body")

	r := NewRegistry(cwd)
	p, ok := r.Get("ml")
	if !ok {
		t.Fatal("ml should resolve")
	}
	if p.Identity != "project body" {
		t.Errorf("Identity = %q, want project to override user", p.Identity)
	}
}
