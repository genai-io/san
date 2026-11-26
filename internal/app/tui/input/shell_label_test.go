package input

import (
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestApprovalRecordedShellTitle(t *testing.T) {
	for _, name := range []string{"Bash", "PowerShell"} {
		for _, label := range []string{"", "bash", "zsh", "pwsh", "PS"} {
			t.Run(name+"/"+label, func(t *testing.T) {
				p := &ApprovalModel{request: &perm.PermissionRequest{ToolName: name, BashMeta: &perm.BashMetadata{ShellLabel: label}}}
				want := label
				if want == "" {
					want = name
				}
				if got := p.getTitle(); got != want+" command" {
					t.Fatalf("title %q", got)
				}
				if p.request.ToolName != name {
					t.Fatal("changed policy tool identity")
				}
			})
		}
	}
}
