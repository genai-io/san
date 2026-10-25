package conv

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestBashQuietTailRollingAndSettled(t *testing.T) {
	for _, width := range []int{32, 40} {
		for _, tc := range []struct{ output, want string }{
			{"one\ntwo\n", "  │ one\n  │ two\n"},
			{"one\ntwo\nthree\n", "  …\n  │ two\n  │ three\n"},
			{"one\ntwo\nthree\nfour\n", "  …\n  │ three\n  │ four\n"},
			{"one\n雪  \n\n", "  …\n  │ 雪  \n  │ \n"},
			{"one\n\n\n", "  …\n  │ \n  │ \n"},
		} {
			if got := xansi.Strip(renderOutputPreview(tc.output, 2, width)); got != tc.want {
				t.Errorf("stream width %d: %q want %q", width, got, tc.want)
			}
			count := strings.Count(tc.output, "\n")
			if count <= 2 {
				continue
			}
			got := xansi.Strip(renderBashToolResultInline(ToolResultData{ToolName: "Bash", Content: tc.output, Width: width, BashPreviewLines: 2, Details: toolresult.BashDetails{LineCount: count, Tail: tc.output}}))
			if got != tc.want {
				t.Errorf("settled width %d: %q want %q", width, got, tc.want)
			}
		}
	}
}

func TestBashQuietTailFailureRetainsDiagnostics(t *testing.T) {
	for _, width := range []int{32, 40} {
		got := xansi.Strip(renderBashToolResultInline(ToolResultData{ToolName: "Bash", Content: "fatal: missing repository\nError: exit code 128", IsError: true, Width: width, BashPreviewLines: 2, Details: toolresult.BashDetails{LineCount: 100, Tail: "fatal: missing repository\n", Error: "exit code 128"}}))
		if strings.Count(got, "  …\n") != 1 || !strings.HasPrefix(got, "  …\n") || !strings.Contains(got, "fatal: missing repository") || !strings.Contains(got, "failed · exit code 128") || strings.Contains(got, "last ") {
			t.Fatal(got)
		}
	}
}
