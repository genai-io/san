package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/perm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestBuildParamsShellHelpIsLocalToSchemas(t *testing.T) {
	const help = "Directory changes apply to this command only"
	bashHelp := func(p BuildParams) string {
		t.Helper()
		for _, schema := range p.Schemas() {
			if schema.Name == "Bash" {
				return schema.Description
			}
		}
		t.Fatal("missing Bash schema")
		return ""
	}
	before := bashHelp(BuildParams{})
	if !strings.Contains(before, "updates the session working directory") {
		t.Fatal("default help lost session directory behavior")
	}
	custom := bashHelp(BuildParams{ShellDirHelp: help})
	if !strings.Contains(custom, help) || strings.Contains(custom, "updates the session working directory") {
		t.Fatal(custom)
	}
	if after := bashHelp(BuildParams{}); after != before {
		t.Fatal("schema override mutated default help")
	}
	for _, schema := range (BuildParams{ShellDirHelp: help, DisabledTools: map[string]bool{"Bash": true}}).Schemas() {
		if schema.Name == "Bash" {
			t.Fatal("help override restored disabled tool")
		}
	}
}

func TestBuildParamsConversationToolState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "observed"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	owner := fs.NewFileViews()
	for _, tc := range []struct {
		name, input string
		views       *fs.FileViews
		wantError   bool
	}{
		{"Read", `{"file_path":"observed"}`, owner, false},
		{"Write", `{"file_path":"observed","content":"updated"}`, owner, false},
		{"Write", `{"file_path":"observed","content":"foreign"}`, fs.NewFileViews(), true},
	} {
		// Rebuilding the runtime must not discard the conversation's observations.
		driver := aitest.New(aitest.Replies(ai.Response{Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "owned-call", Name: tc.name, Input: tc.input})}, StopReason: ai.StopToolUse}), aitest.Says("finished"))
		var response any
		ended := false
		p := BuildParams{Provider: runOnceProvider{driver}, ModelID: "fake", CWD: dir,
			FileViews:       tc.views,
			PermissionRules: func(string, map[string]any) PermDecisionResult { return PermDecisionResult{Decision: perm.Permit} },
			ToolResponse: func(id string, payload any) {
				if id != "owned-call" {
					t.Errorf("call ID = %q", id)
				}
				response = payload
			},
			OnEvent: func(event core.Event) {
				if e, ok := event.(sdkagent.ToolEnd); ok {
					ended = true
					if (e.Err != nil) != tc.wantError {
						t.Errorf("%s error = %v", tc.name, e.Err)
					}
					if !tc.wantError && response == nil {
						t.Error("ToolEnd preceded hook response handoff")
					}
				}
			},
		}
		if _, err := RunOnce(context.Background(), p, core.UserMessage("run tool", nil), nil); err != nil {
			t.Fatal(err)
		}
		if !ended {
			t.Fatal("tool did not run")
		}
		if tool.PopSideEffect("owned-call") != nil {
			t.Fatal("response leaked to TUI store")
		}
	}
}
