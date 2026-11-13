package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPInitialCommandDiscovery(t *testing.T) {
	f := newACPFixture(t, aitest.Always(aitest.Says("done")), nil)
	cwd := t.TempDir()
	path := filepath.Join(cwd, ".san", "commands", "check.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\ndescription: Check project\n---\nCheck project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	id := f.session(t, cwd)
	var response protocol.SetSessionModeResponse
	if err := f.client.Call(f.ctx, "session/set_mode", protocol.SetSessionModeRequest{SessionID: id, ModeID: "normal"}, &response); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, update := range f.updates {
		if update.SessionID != id {
			continue
		}
		if catalog, ok := update.Update.Value.(protocol.SessionUpdateAvailableCommandsUpdateVariant); ok {
			for _, command := range catalog.AvailableCommands {
				if command.Name == "check" {
					return
				}
			}
		}
	}
	t.Fatal("new session did not advertise /check before its first prompt")
}

func TestACPCommandCatalogUpdates(t *testing.T) {
	f := newACPFixture(t, aitest.Always(aitest.Says("done")), nil)
	cwd := t.TempDir()
	id := f.session(t, cwd)
	s := f.host.sessions[id]
	check := func(id protocol.SessionID, want int) {
		t.Helper()
		if _, err := f.prompt(id, "hello"); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		got := 0
		for _, u := range f.updates {
			if _, ok := u.Update.Value.(protocol.SessionUpdateAvailableCommandsUpdateVariant); ok && u.SessionID == id {
				got++
			}
		}
		if got != want {
			t.Fatalf("command updates = %d, want %d", got, want)
		}
	}
	check(id, 1)
	check(id, 1)
	if err := s.runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	check(id, 1)
	check(f.session(t, t.TempDir()), 1)

	path := filepath.Join(cwd, ".san", "commands", "check.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	for i, description := range []string{"First description", "Changed description"} {
		if err := os.WriteFile(path, []byte("---\ndescription: "+description+"\n---\nCheck the project\n"), 0600); err != nil {
			t.Fatal(err)
		}
		s.commands = command.NewRegistry(command.Options{CWD: cwd})
		check(id, i+2)
		check(id, i+2)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.commands = command.NewRegistry(command.Options{CWD: cwd})
	check(id, 4)
}

func TestACPPluginCommandsAndAgents(t *testing.T) {
	var seen atomic.Bool
	f := newACPFixture(t, aitest.New(aitest.Says("command completed")), nil)
	cwd := t.TempDir()
	files := map[string]string{
		".san/settings.local.json":                  `{"enabledPlugins":{"demo":true}}`,
		".san/plugins/demo/.san-plugin/plugin.json": `{"name":"demo","version":"1.0.0"}`,
		".san/plugins/demo/commands/check.md":       "---\ndescription: Check the project\n---\nPLUGIN_COMMAND_BODY\n",
		".san/plugins/demo/agents/research.md":      "---\nname: research\ndescription: plugin researcher\n---\nPLUGIN_AGENT_BODY\n",
	}
	for path, body := range files {
		p := filepath.Join(cwd, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	id := f.session(t, cwd)
	other := f.session(t, t.TempDir())
	s := f.host.sessions[id]
	if _, ok := s.agents.Get("demo:research"); !ok {
		t.Fatal("plugin agent not loaded")
	}
	if _, ok := f.host.sessions[other].agents.Get("demo:research"); ok {
		t.Fatal("agent leaked across sessions")
	}
	if _, err := f.prompt(id, "/demo:check arguments"); err != nil {
		t.Fatal(err)
	}
	history, err := s.nativeSession.Store.Read(string(id), session.LegacyReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range history.Messages {
		if strings.Contains(m.Text(), "PLUGIN_COMMAND_BODY") && strings.Contains(m.Text(), "/demo:check arguments") {
			seen.Store(true)
		}
	}
	if !seen.Load() {
		t.Fatal("native command instructions missing from history")
	}
	if _, err := f.prompt(other, "/demo:check"); err == nil {
		t.Fatal("foreign command executed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	advertised := false
	for _, u := range f.updates {
		if update, ok := u.Update.Value.(protocol.SessionUpdateAvailableCommandsUpdateVariant); ok {
			for _, c := range update.AvailableCommands {
				if c.Name == "demo:check" {
					advertised = true
				}
			}
		}
	}
	if !advertised {
		t.Fatal("ACP command catalog missing")
	}
}
