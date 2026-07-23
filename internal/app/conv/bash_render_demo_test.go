package conv

import (
	"fmt"
	"os"
	"testing"

	"github.com/genai-io/san/internal/core"
)

// TestBashRenderDemo prints how Bash tool calls render, for eyeballing.
// Run:  SAN_BASH_RENDER_DEMO=1 go test ./internal/app/conv/ -run TestBashRenderDemo -v
// Tweak the `width` and add your own cases to `samples`.
func TestBashRenderDemo(t *testing.T) {
	if os.Getenv("SAN_BASH_RENDER_DEMO") == "" {
		t.Skip("set SAN_BASH_RENDER_DEMO=1 to print the Bash rendering demo")
	}

	const width = 70 // terminal width; block wraps at ~80% of this

	samples := []struct {
		name  string
		input string
	}{
		{
			"short single-line with dimmed description",
			`{"command":"git status","description":"Inspect repository state"}`,
		},
		{
			"long single-line (truncated preview)",
			`{"command":"git log --oneline --graph --all --decorate --abbrev-commit | head -50"}`,
		},
		{
			"multi-line for-loop + description",
			`{"command":"for f in $(git diff --name-only); do\n  gofmt -w \"$f\"\ndone","description":"Format changed Go files"}`,
		},
		{
			"multi-line, no description",
			`{"command":"set -e\nmake build\nmake test\necho done"}`,
		},
		{
			"blank line in the middle is kept",
			`{"command":"echo start\n\necho end"}`,
		},
		{
			"very long unbroken token (hard-wrapped)",
			`{"command":"curl -sSL https://example.com/some/really/long/path/that/keeps/going/and/going/and/will/not/fit"}`,
		},
		{
			"CJK / wide chars",
			`{"command":"echo 你好世界，这是一个比较长的中文命令用来测试换行宽度是否正确"}`,
		},
	}

	fmt.Printf("\n==== Bash tool-call rendering (width=%d) ====\n\n", width)
	for _, s := range samples {
		params := ToolCallsParams{
			ToolCalls: []core.ToolCall{{ID: "tc", Name: "Bash", Input: s.input}},
			ResultMap: map[string]ToolResultData{},
			Width:     width,
		}
		fmt.Printf("--- %s ---\n", s.name)
		fmt.Print(RenderToolCalls(params))
		fmt.Println()
	}
}
