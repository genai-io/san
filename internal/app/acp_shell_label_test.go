package app

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPShellPermissionTitle(t *testing.T) {
	shell, err := proc.DefaultShell()
	if err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "shell", Name: shell.Kind.ToolName(), Input: `{"command":"exit 0"}`}), aitest.Says("done"))
	requests := 0
	f := newACPFixture(t, driver, func(_ context.Context, req protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		requests++
		if req.ToolCall.Title != protocol.Some(shell.Label()) {
			t.Errorf("permission title = %v, want %q", req.ToolCall.Title, shell.Label())
		}
		return acpChoice("reject_once"), nil
	})
	id := f.session(t, t.TempDir())
	if _, err := f.prompt(id, "run"); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("permission requests = %d, want 1", requests)
	}
}

func TestACPShellTitles(t *testing.T) {
	for _, name := range []string{"Bash", "PowerShell"} {
		for _, label := range []string{"", "bash", "zsh", "pwsh", "PS"} {
			t.Run(name+"/"+label, func(t *testing.T) {
				for _, status := range []protocol.ToolCallStatus{protocol.ToolCallStatusInProgress, protocol.ToolCallStatusCompleted} {
					update := acpToolUpdate("call", name, "", status, toolresult.BashDetails{ShellLabel: label})
					value := update.Value.(protocol.SessionUpdateToolCallUpdateVariant)
					want := label
					if want == "" {
						want = name
					}
					if got := value.Title; got != protocol.Some(want) {
						t.Fatalf("title %#v, want %q", got, want)
					}
				}
			})
		}
	}
}
