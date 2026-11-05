package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

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
	history, err := s.data.Store.Read(string(id))
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
