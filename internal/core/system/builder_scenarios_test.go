package system

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
)

var update = flag.Bool("update", false, "update golden files in testdata/")

// scenario describes a Build invocation. The opts func centralizes how a
// scenario is built so golden tests and assertion tests share the same setup.
type scenario struct {
	name  string
	scope core.Scope
	opts  func() []Option
}

func testScenarios() []scenario {
	// The scenario cwd is not a real repo on the test machine, so the branch
	// line is deterministically absent from golden files.
	mainEnv := Environment{Cwd: "/home/user/myproject"}
	subEnv := Environment{Cwd: "/home/user/myproject"}

	return []scenario{
		{
			name:  "main_session",
			scope: core.ScopeMain,
			opts: func() []Option {
				return []Option{
					WithEnvironment(mainEnv),
				}
			},
		},
		{
			name:  "subagent_readonly",
			scope: core.ScopeSubagent,
			opts: func() []Option {
				return []Option{
					WithSubagentIdentity(SubagentBrief{
						AgentName:   "subagent",
						Description: "General-purpose subagent for research and multi-step tasks.",
						Mode:        "explore",
					}),
					WithEnvironment(subEnv),
				}
			},
		},
		{
			name:  "subagent_general",
			scope: core.ScopeSubagent,
			opts: func() []Option {
				return []Option{
					WithSubagentIdentity(SubagentBrief{
						AgentName:    "subagent",
						Description:  "General-purpose subagent for research and execution.",
						Mode:         "default",
						CustomPrompt: "Focus on minimal, surgical fixes.",
					}),
					WithEnvironment(subEnv),
				}
			},
		},
	}
}

// normalizePrompt replaces dynamic content (date, platform) with stable placeholders.
func normalizePrompt(s string) string {
	dateRe := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	s = dateRe.ReplaceAllString(s, "YYYY-MM-DD")
	platform := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	s = strings.ReplaceAll(s, platform, "PLATFORM/ARCH")
	return s
}

func TestUpdateGoldenFiles(t *testing.T) {
	if !*update {
		t.Skip("use -update to regenerate golden files")
	}

	for _, sc := range testScenarios() {
		sys := Build(sc.scope, sc.opts()...)
		prompt := normalizePrompt(sys.Prompt())

		path := filepath.Join("testdata", sc.name+".txt")
		if err := os.WriteFile(path, []byte(prompt), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(prompt))
	}
}

func TestGoldenFiles(t *testing.T) {
	for _, sc := range testScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			path := filepath.Join("testdata", sc.name+".txt")
			golden, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden file %s: %v (run with -update to generate)", path, err)
			}

			sys := Build(sc.scope, sc.opts()...)
			got := normalizePrompt(sys.Prompt())

			if got != string(golden) {
				t.Errorf("prompt mismatch for scenario %q (run with -update to regenerate)\n\ngot length:  %d\nwant length: %d",
					sc.name, len(got), len(golden))
			}
		})
	}
}

// --- Section presence/absence integration tests ---

func TestScenarioMainSession_HasAllSections(t *testing.T) {
	for _, sc := range testScenarios() {
		if sc.name != "main_session" {
			continue
		}
		sys := Build(sc.scope, sc.opts()...)
		prompt := sys.Prompt()

		required := []struct {
			label   string
			content string
		}{
			{"identity", "You are a coding agent"},
			{"environment", "<environment>"},
			// Memory, skills, agents intentionally absent from system prompt:
			//   - <memory> rides on user messages as <system-reminder>
			//   - <skills> rides on user messages as <system-reminder>
			//   - agent directory lives in the Agent tool's description
			{"core rules", "## Safety"},
		}
		for _, r := range required {
			if !strings.Contains(prompt, r.content) {
				t.Errorf("main session should contain %s (%q)", r.label, r.content)
			}
		}
		for _, gone := range []string{"<memory", "<skills>", "<agents>"} {
			if strings.Contains(prompt, gone) {
				t.Errorf("main session should NOT contain %q", gone)
			}
		}
	}
}

func TestScenarioSubagentReadonly_NoMainOnlyGuidelines(t *testing.T) {
	for _, sc := range testScenarios() {
		if sc.name != "subagent_readonly" {
			continue
		}
		sys := Build(sc.scope, sc.opts()...)
		prompt := sys.Prompt()

		if !strings.Contains(prompt, "You are a subagent") {
			t.Error("should announce subagent identity")
		}
		if !strings.Contains(prompt, `mode="explore"`) {
			t.Error("identity tag should carry explore mode attribute")
		}
		if !strings.Contains(prompt, "do not modify files") {
			t.Error("explore identity should prohibit file mutation")
		}
		if !strings.Contains(prompt, "shell commands are limited to operations classified as read-only") {
			t.Error("explore identity should permit only read-only shell operations")
		}
		if strings.Contains(prompt, "do not modify files or run shell commands") {
			t.Error("explore identity should not prohibit all shell commands")
		}
		if strings.Contains(prompt, "<behavior>") {
			t.Error("subagent should NOT have the behavior part")
		}
	}
}

func TestScenarioSubagentGeneral_NoCapabilitiesInSystemPrompt(t *testing.T) {
	for _, sc := range testScenarios() {
		if sc.name != "subagent_general" {
			continue
		}
		sys := Build(sc.scope, sc.opts()...)
		prompt := sys.Prompt()

		if !strings.Contains(prompt, "You are a subagent") {
			t.Error("should announce subagent identity")
		}
		// Skills now ride on the subagent's first user message as a
		// <system-reminder> instead of appearing in the system prompt.
		if strings.Contains(prompt, "<skills>") {
			t.Error("subagent system prompt should NOT have skills section")
		}
		// Subagents do not recursively spawn subagents — no agents section.
		if strings.Contains(prompt, "<agents>") {
			t.Error("subagent should NOT have agents section by default")
		}
	}
}
