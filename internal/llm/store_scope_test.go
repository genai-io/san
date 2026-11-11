package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func scopeStore(t *testing.T, cwd string) *Store {
	t.Helper()
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	s, err := NewStoreForCwd(cwd)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func scopeSelect(t *testing.T, s *Store, model CurrentModelInfo) {
	t.Helper()
	if err := s.SetCurrentModel(model.ModelID, model.Provider, model.AuthMethod); err != nil {
		t.Fatal(err)
	}
}

func scopeWant(t *testing.T, s *Store, want *CurrentModelInfo) {
	t.Helper()
	if got := s.CurrentModel(); !reflect.DeepEqual(got, want) {
		t.Fatalf("CurrentModel() = %#v, want %#v", got, want)
	}
}

func scopeWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func scopeRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStoreScopeDefaultAndProjectIsolation(t *testing.T) {
	home, a, b := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	first := CurrentModelInfo{"default", OpenAI, AuthAPIKey}
	override := CurrentModelInfo{"shared-id", Moonshot, AuthMethod("oauth")}
	s := scopeStore(t, a)
	scopeSelect(t, s, first)
	projectPath := filepath.Join(a, ".san", "providers.json")
	if _, err := os.Stat(projectPath); !os.IsNotExist(err) {
		t.Fatalf("first selection wrote project file: %v", err)
	}
	userPath := filepath.Join(home, ".san", "providers.json")
	original := string(scopeRead(t, userPath))
	scopeSelect(t, s, override)
	if got := string(scopeRead(t, userPath)); got != original {
		t.Fatal("project selection changed user store")
	}
	var project map[string]json.RawMessage
	if err := json.Unmarshal(scopeRead(t, projectPath), &project); err != nil {
		t.Fatal(err)
	}
	if len(project) != 1 || project["current"] == nil {
		t.Fatalf("project keys = %v, want only current", project)
	}
	var persisted CurrentModelInfo
	if err := json.Unmarshal(project["current"], &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != override {
		t.Fatalf("persisted selection = %#v, want %#v", persisted, override)
	}
	scopeWant(t, scopeStore(t, a), &override)
	scopeWant(t, scopeStore(t, b), &first)
	userOnly, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	scopeWant(t, userOnly, &first)
	if err := s.Connect(OpenAI, AuthAPIKey); err != nil {
		t.Fatal(err)
	}
	if err := s.CacheModels(OpenAI, AuthAPIKey, []ModelInfo{{ID: "default", DisplayName: "Default"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetThinkingEffort("default", "high"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContextLimit("default", 100, 20); err != nil {
		t.Fatal(err)
	}
	other := scopeStore(t, b)
	if !other.IsConnected(OpenAI, AuthAPIKey) || other.CachedModelDisplayName("default") != "Default" || other.ThinkingEffort("default") != "high" {
		t.Fatal("connections/cache/preferences were not user-wide")
	}
	if in, out, ok := other.ContextLimit("default"); !ok || in != 100 || out != 20 {
		t.Fatal("context limit was not user-wide")
	}
	var after map[string]json.RawMessage
	if err := json.Unmarshal(scopeRead(t, projectPath), &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project, after) {
		t.Fatal("user-wide writes changed project store")
	}
}

func TestStoreProjectReloadAndFallback(t *testing.T) {
	for _, mode := range []string{"clear", "delete", "null", "empty"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			def := CurrentModelInfo{"default", OpenAI, AuthAPIKey}
			over := CurrentModelInfo{"project", Moonshot, AuthAPIKey}
			writer := scopeStore(t, cwd)
			scopeSelect(t, writer, def)
			reader := scopeStore(t, cwd)
			scopeSelect(t, writer, over)
			if err := reader.Reload(); err != nil {
				t.Fatal(err)
			}
			scopeWant(t, reader, &over)
			path := filepath.Join(cwd, ".san", "providers.json")
			switch mode {
			case "clear":
				if err := writer.ClearCurrentModel(); err != nil {
					t.Fatal(err)
				}
				scopeWant(t, writer, &def)
			case "delete":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "null":
				scopeWrite(t, path, `{"current":null}`)
			case "empty":
				scopeWrite(t, path, `{}`)
			}
			if err := reader.Reload(); err != nil {
				t.Fatal(err)
			}
			scopeWant(t, reader, &def)
			scopeWant(t, scopeStore(t, cwd), &def)
			if err := os.Remove(filepath.Join(home, ".san", "providers.json")); err != nil {
				t.Fatal(err)
			}
			if err := reader.Reload(); err != nil {
				t.Fatal(err)
			}
			scopeWant(t, reader, nil)
		})
	}
}

func TestStoreScopeProjectIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, linked := t.TempDir(), t.TempDir()
	scopeWrite(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	scopeWrite(t, filepath.Join(linked, ".git"), "gitdir: "+filepath.Join(root, ".git", "worktrees", "linked")+"\n")
	sub := filepath.Join(root, "nested", "dir")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	def := CurrentModelInfo{"default", OpenAI, AuthAPIKey}
	over := CurrentModelInfo{"project", Moonshot, AuthAPIKey}
	s := scopeStore(t, sub)
	scopeSelect(t, s, def)
	scopeSelect(t, s, over)
	for _, cwd := range []string{root, sub, filepath.Join(alias, "nested", "dir")} {
		scopeWant(t, scopeStore(t, cwd), &over)
	}
	scopeWant(t, scopeStore(t, linked), &def)
	scopeSelect(t, scopeStore(t, linked), CurrentModelInfo{"linked", OpenAI, AuthAPIKey})
	scopeWant(t, scopeStore(t, root), &over)
}

func TestStoreSelectionStaleFirstRunPreservesDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	stale := scopeStore(t, cwd)
	def := CurrentModelInfo{"new-default", OpenAI, AuthAPIKey}
	over := CurrentModelInfo{"stale-choice", Moonshot, AuthAPIKey}
	scopeSelect(t, scopeStore(t, ""), def)
	if err := stale.SetThinkingEffort("new-default", "high"); err != nil {
		t.Fatal(err)
	}
	scopeWant(t, scopeStore(t, ""), &def)
	scopeSelect(t, stale, over)
	scopeWant(t, stale, &over)
	scopeWant(t, scopeStore(t, ""), &def)
	scopeWant(t, scopeStore(t, cwd), &over)
}

func TestStoreSelectionPersistenceFailure(t *testing.T) {
	for _, target := range []string{"user", "project"} {
		t.Run(target, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			s := scopeStore(t, cwd)
			var want *CurrentModelInfo
			blocked := filepath.Join(home, ".san", "providers.json")
			if target == "project" {
				def := CurrentModelInfo{"default", OpenAI, AuthAPIKey}
				scopeSelect(t, s, def)
				want = &def
				blocked = filepath.Join(cwd, ".san", "providers.json")
			}
			// A directory at the file path fails reliably even when tests run as root.
			if err := os.MkdirAll(blocked, 0755); err != nil {
				t.Fatal(err)
			}
			if err := s.SetCurrentModel("failed", Moonshot, AuthAPIKey); err == nil {
				t.Fatal("selection unexpectedly succeeded")
			}
			scopeWant(t, s, want)
		})
	}
}

func TestStoreScopeHomeUsesOnlyUserStore(t *testing.T) {
	home := t.TempDir()
	alias := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", alias)
	s := scopeStore(t, home)
	if err := s.Connect(OpenAI, AuthAPIKey); err != nil {
		t.Fatal(err)
	}
	scopeSelect(t, s, CurrentModelInfo{"first", OpenAI, AuthAPIKey})
	next := CurrentModelInfo{"next", OpenAI, AuthSubscription}
	scopeSelect(t, s, next)
	user := scopeStore(t, "")
	scopeWant(t, user, &next)
	if !user.IsConnected(OpenAI, AuthAPIKey) {
		t.Fatal("home project selection overwrote user connections")
	}
}

func TestStoreScopeReseedsRemovedUserDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	s := scopeStore(t, cwd)
	scopeSelect(t, s, CurrentModelInfo{"default", OpenAI, AuthAPIKey})
	scopeSelect(t, s, CurrentModelInfo{"project", Moonshot, AuthAPIKey})
	if err := scopeStore(t, "").ClearCurrentModel(); err != nil {
		t.Fatal(err)
	}
	next := CurrentModelInfo{"new-default", OpenAI, AuthSubscription}
	scopeSelect(t, s, next)
	scopeWant(t, s, &next)
	scopeWant(t, scopeStore(t, ""), &next)
	scopeWant(t, scopeStore(t, cwd), &next)
}

func TestStoreSelectionMalformedFilesPreserved(t *testing.T) {
	for _, target := range []string{"user", "project"} {
		t.Run(target, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			s := scopeStore(t, cwd)
			def := CurrentModelInfo{"default", OpenAI, AuthAPIKey}
			scopeSelect(t, s, def)
			path := filepath.Join(home, ".san", "providers.json")
			if target == "project" {
				path = filepath.Join(cwd, ".san", "providers.json")
			}
			const malformed = `{"current":`
			scopeWrite(t, path, malformed)
			if _, err := NewStoreForCwd(cwd); err == nil {
				t.Fatal("opening malformed store succeeded")
			}
			if err := s.Reload(); err == nil {
				t.Fatal("reloading malformed store succeeded")
			}
			if err := s.SetCurrentModel("replacement", Moonshot, AuthAPIKey); err == nil {
				t.Fatal("selection overwrote malformed store")
			}
			if err := s.ClearCurrentModel(); err == nil {
				t.Fatal("clear overwrote malformed store")
			}
			scopeWant(t, s, &def)
			if got := string(scopeRead(t, path)); got != malformed {
				t.Fatalf("malformed content changed to %q", got)
			}
		})
	}
}
