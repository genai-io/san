package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginSourceUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want PluginSource
	}{
		{"relative path string", `"./plugins/foo"`, PluginSource{Type: SourcePath, Path: "./plugins/foo"}},
		{"github", `{"source":"github","repo":"owner/repo","ref":"main"}`, PluginSource{Type: SourceGitHub, Repo: "owner/repo", Ref: "main"}},
		{"url", `{"source":"url","url":"https://x.com/p.git"}`, PluginSource{Type: SourceURL, URL: "https://x.com/p.git"}},
		{"git alias maps to url", `{"source":"git","url":"https://x.com/p.git"}`, PluginSource{Type: SourceURL, URL: "https://x.com/p.git"}},
		{"git-subdir", `{"source":"git-subdir","url":"https://x.com/m.git","path":"tools/p","sha":"abc"}`, PluginSource{Type: SourceGitSubdir, URL: "https://x.com/m.git", Path: "tools/p", SHA: "abc"}},
		{"npm", `{"source":"npm","package":"@a/b","version":"^2.0.0"}`, PluginSource{Type: SourceNPM, Package: "@a/b", Version: "^2.0.0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got PluginSource
			if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPluginSourceExternal(t *testing.T) {
	for _, typ := range []string{SourceGitHub, SourceURL, SourceGitSubdir, SourceNPM} {
		if !(PluginSource{Type: typ}).External() {
			t.Errorf("%s should be external", typ)
		}
	}
	if (PluginSource{Type: SourcePath}).External() {
		t.Error("path should not be external")
	}
}

func TestNormalizeGitURL(t *testing.T) {
	cases := map[string]string{
		"owner/repo":             "https://github.com/owner/repo.git",
		"https://x.com/p.git":    "https://x.com/p.git",
		"git@github.com:o/r.git": "git@github.com:o/r.git",
		"":                       "",
	}
	for in, want := range cases {
		if got := normalizeGitURL(in); got != want {
			t.Errorf("normalizeGitURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// writeMarketplaceJSON writes a raw .claude-plugin/marketplace.json so tests
// exercise the real parse path (PluginSource has no symmetric MarshalJSON).
func writeMarketplaceJSON(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(marketplace.json): %v", err)
	}
}

// TestMarketplacePlugins_ManifestFirst checks that declared plugins are read
// from marketplace.json — including external sources that aren't vendored — so
// the "available" count reflects the manifest, not the on-disk directory layout.
func TestMarketplacePlugins_ManifestFirst(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	marketRoot := filepath.Join(t.TempDir(), "market")
	writeTestPlugin(t, filepath.Join(marketRoot, "plugins", "local-tool"), "local-tool", "1.0.0", "Local tool", nil)
	writeMarketplaceJSON(t, marketRoot, `{
		"name": "mixed",
		"plugins": [
			{"name": "local-tool", "source": "./plugins/local-tool", "description": "Local tool"},
			{"name": "remote-tool", "source": {"source": "url", "url": "https://example.com/remote.git"}, "description": "Remote tool", "version": "2.0.0"}
		]
	}`)

	m := NewMarketplaceManager(tmp)
	if err := m.Add("mixed", MarketplaceEntry{Source: MarketplaceSourceInfo{Source: "directory", Path: marketRoot}}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	plugins, err := m.MarketplacePlugins("mixed")
	if err != nil {
		t.Fatalf("MarketplacePlugins: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("got %d plugins, want 2: %+v", len(plugins), plugins)
	}
	byName := map[string]MarketplacePlugin{}
	for _, p := range plugins {
		byName[p.Name] = p
	}
	if byName["local-tool"].Source.Type != SourcePath {
		t.Errorf("local-tool source = %+v, want path", byName["local-tool"].Source)
	}
	remote := byName["remote-tool"]
	if !remote.Source.External() || remote.Source.URL != "https://example.com/remote.git" {
		t.Errorf("remote-tool source = %+v, want external url", remote.Source)
	}
	if remote.Version != "2.0.0" {
		t.Errorf("remote-tool version = %q, want 2.0.0", remote.Version)
	}
}

// TestMarketplacePlugins_DirScanFallback covers a marketplace with no
// marketplace.json: plugins are discovered by scanning vendored subdirectories
// and enriched from each plugin's own manifest.
func TestMarketplacePlugins_DirScanFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	marketRoot := filepath.Join(t.TempDir(), "market")
	writeTestPlugin(t, filepath.Join(marketRoot, "plugins", "alpha"), "alpha", "1.0.0", "Alpha", nil)

	m := NewMarketplaceManager(tmp)
	if err := m.Add("scan", MarketplaceEntry{Source: MarketplaceSourceInfo{Source: "directory", Path: marketRoot}}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	plugins, err := m.MarketplacePlugins("scan")
	if err != nil {
		t.Fatalf("MarketplacePlugins: %v", err)
	}
	if len(plugins) != 1 || plugins[0].Name != "alpha" {
		t.Fatalf("got %+v, want one alpha plugin", plugins)
	}
	if plugins[0].Description != "Alpha" {
		t.Errorf("description = %q, want Alpha (enriched from disk)", plugins[0].Description)
	}
}

// TestInstall_RelativePathSourceFromManifest installs a plugin whose
// marketplace.json source is a relative path inside the marketplace repo,
// verifying the manifest-driven local-path resolution.
func TestInstall_RelativePathSourceFromManifest(t *testing.T) {
	tmpHome := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", tmpHome)

	marketRoot := filepath.Join(t.TempDir(), "market")
	writeTestPlugin(t, filepath.Join(marketRoot, "plugins", "deploy"), "deploy", "1.2.3", "Deploy plugin", map[string]string{
		"skills/deploy/SKILL.md": "---\nname: deploy\ndescription: Deploy skill\n---\nship it\n",
	})
	writeMarketplaceJSON(t, marketRoot, `{
		"name": "local-market",
		"plugins": [
			{"name": "deploy", "source": "./plugins/deploy", "description": "Deploy plugin"}
		]
	}`)

	mgr := NewMarketplaceManager(cwd)
	if err := mgr.Add("local-market", MarketplaceEntry{Source: MarketplaceSourceInfo{Source: "directory", Path: marketRoot}}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	registry := NewRegistry()
	if err := Install(context.Background(), registry, cwd, "deploy@local-market", ScopeProject); err != nil {
		t.Fatalf("Install: %v", err)
	}

	installPath := filepath.Join(cwd, ".san", "plugins", "deploy")
	if _, err := os.Stat(filepath.Join(installPath, "skills", "deploy", "SKILL.md")); err != nil {
		t.Fatalf("expected installed plugin content copied: %v", err)
	}
	if _, ok := registry.Get("deploy@local-market"); !ok {
		t.Fatal("expected installed plugin registered")
	}
}
