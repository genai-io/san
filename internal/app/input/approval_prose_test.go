package input

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool/perm"
)

func TestBashApprovalWrapsDescriptionSeparatelyFromCommand(t *testing.T) {
	const width = 12
	meta := &perm.BashMetadata{
		Command:     "printf '\t中文abcdef next'",
		Description: "中文abcdef next\n\nDone",
	}
	wantCommand := strings.TrimSuffix(kit.RenderCommandBlock(meta.Command, width), "\n")
	got := newApprovalBashPreview(meta).render(width)
	prose, ok := strings.CutPrefix(got, wantCommand+"\n")
	if !ok {
		t.Fatalf("command rendering changed: %q", got)
	}
	if got := ansi.Strip(prose); got != "中文abcdef\nnext\n\nDone" {
		t.Fatalf("description wrapping = %q", got)
	}
}
