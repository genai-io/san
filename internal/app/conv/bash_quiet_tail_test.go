package conv

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestBashQuietTailRollingAndSettled(t *testing.T) {
	for _, width := range []int{32, 40} {
		for _, tc := range []struct{ output, live, settled string }{
			{"one\ntwo\n", "    │ one\n    │ two\n", "    │ one\n    └ two\n"},
			{"one\ntwo", "    │ one\n    │ two\n", "    │ one\n    └ two\n"},
			{"one\ntwo\nthree\n", "    │ … (1)\n    │ two\n    │ three\n", "    │ … (1)\n    │ two\n    └ three\n"},
			{"one\ntwo\nthree\nfour\n", "    │ … (2)\n    │ three\n    │ four\n", "    │ … (2)\n    │ three\n    └ four\n"},
			{"one\n雪  \n\n", "    │ … (1)\n    │ 雪  \n    │ \n", "    │ … (1)\n    │ 雪  \n    └ \n"},
			{"one\n\n\n", "    │ … (1)\n    │ \n    │ \n", "    │ … (1)\n    │ \n    └ \n"},
		} {
			details := toolresult.BashDetails{LineCount: bashOutputLineCount(tc.output), Tail: tc.output}
			if got := xansi.Strip(renderOutputPreview(tc.output, 2, width, details, false)); got != tc.live {
				t.Errorf("stream width %d: %q want %q", width, got, tc.live)
			}
			got := xansi.Strip(renderBashToolResultInline(ToolResultData{ToolName: "Bash", Content: tc.output, Width: width, BashPreviewLines: 2, Details: details}))
			if got != tc.settled {
				t.Errorf("settled width %d: %q want %q", width, got, tc.settled)
			}
		}
	}
}

func TestBashQuietTailFailureRetainsDiagnostics(t *testing.T) {
	for _, width := range []int{32, 40} {
		got := xansi.Strip(renderBashToolResultInline(ToolResultData{ToolName: "Bash", Content: "fatal: missing repository\nError: exit code 128", IsError: true, Width: width, BashPreviewLines: 2, Details: toolresult.BashDetails{LineCount: 100, Tail: "fatal: missing repository\n", Error: "exit code 128"}}))
		if strings.Count(got, "│ … (99)\n") != 1 || !strings.HasPrefix(got, "    │ … (99)\n") || !strings.Contains(got, "fatal: missing repository") || !strings.Contains(got, "└ failed · exit code 128") || strings.Contains(got, "last ") {
			t.Fatal(got)
		}
	}
}
