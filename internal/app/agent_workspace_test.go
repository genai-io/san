package app

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/genai-io/san/internal/judge"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

func TestBuildAgentParamsLiveReviewWorkspace(t *testing.T) {
	m, _ := resumedModel(t, nil)
	m.userInput = fixedComposerModel(80, 24).userInput
	m.services.Tool = tool.NewRegistry()
	m.services.MCP = mcp.NewManagerForTest(nil)
	m.judgeApprovals, m.judgeEscalations = new(atomic.Int64), new(atomic.Int64)
	gitCwd, plainCwd := t.TempDir(), t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", gitCwd).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	m.env.CWD, m.env.IsGit = gitCwd, true
	m.learnedStores = newLearnedStoreContext(gitCwd, m.services.Setting)
	m.env.SessionPermissions.SetMode(setting.ModeAutoPilot)
	provider := &autopilotStubProvider{content: `{"decision":"escalate","reason":"test"}`}
	m.env.LLMProvider = provider
	params, err := m.buildAgentParams()
	if err != nil {
		t.Fatal(err)
	}
	owner := m.services.Session
	before := m.autopilot.Load().judge
	check := func(cwd string, underGit bool) {
		t.Helper()
		params.PermissionReview(context.Background(), "Write", map[string]any{"file_path": "file.txt", "content": "test"}, "test")
		// Capture the actual Request evidence rendered by the judge, not a
		// parallel test-only permission closure.
		text := provider.lastOptions().Messages[0].Text()
		start := strings.LastIndex(text, "\n{")
		if start < 0 {
			t.Fatalf("missing request: %s", text)
		}
		var wire struct {
			CWD      string `json:"workingDirectory"`
			UnderGit bool   `json:"workingDirectoryUnderGit"`
		}
		if err := json.NewDecoder(strings.NewReader(text[start:])).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		request := judge.Request{CWD: wire.CWD, UnderGit: wire.UnderGit}
		if request.CWD != cwd || request.UnderGit != underGit {
			t.Fatalf("review workspace = %+v, want %s/%v", request, cwd, underGit)
		}
		if params.CWDFunc() != cwd {
			t.Fatal("tools and judge disagree")
		}
	}
	check(gitCwd, true)
	m.changeCwd(plainCwd)
	check(plainCwd, false)
	m.changeCwd(gitCwd)
	check(gitCwd, true)
	if m.services.Session != owner || params.SessionID != owner.ID() || m.autopilot.Load().judge != before {
		t.Fatal("workspace switch replaced session or judge")
	}
}

func TestAutopilotWorkspaceSnapshotConcurrent(t *testing.T) {
	m, _ := resumedModel(t, nil)
	m.services.MCP = mcp.NewManagerForTest(nil)
	m.judgeApprovals, m.judgeEscalations = new(atomic.Int64), new(atomic.Int64)
	m.env.SessionPermissions.SetMode(setting.ModeAutoPilot)
	provider := &autopilotStubProvider{content: `{"decision":"escalate","reason":"test"}`}
	m.env.LLMProvider = provider
	m.env.CWD, m.env.IsGit = "git", true
	params, err := m.buildAgentParams()
	if err != nil {
		t.Fatal(err)
	}
	start, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		<-start
		for range 200 {
			params.PermissionReview(context.Background(), "Write", map[string]any{"file_path": "file.txt"}, "test")
			text := provider.lastOptions().Messages[0].Text()
			var wire struct {
				CWD      string `json:"workingDirectory"`
				UnderGit bool   `json:"workingDirectoryUnderGit"`
			}
			at := strings.LastIndex(text, "\n{")
			if at < 0 {
				t.Error("missing request")
				return
			}
			if err := json.NewDecoder(strings.NewReader(text[at:])).Decode(&wire); err != nil {
				t.Error(err)
				return
			}
			if wire.UnderGit != (wire.CWD == "git") {
				t.Errorf("torn workspace: %+v", wire)
				return
			}
			_ = params.CWDFunc()
		}
	}()
	close(start)
	for range 200 {
		m.env.CWD, m.env.IsGit = "plain", false
		m.refreshAutopilotSnapshot()
		m.env.CWD, m.env.IsGit = "git", true
		m.refreshAutopilotSnapshot()
	}
	<-done
}
