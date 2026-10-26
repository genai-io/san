package fs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestExactProgressSDKShell(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		rows          int
		truncated     bool
	}{
		{"newline-free", "printf hello; sleep 0.2", 1, false},
		{"partial-row", "printf 'one\\ntwo'", 2, false},
		{"byte-cap", `awk 'BEGIN { for(i=0;i<100;i++) { for(j=0;j<3000;j++) printf "x"; print "" } }'; sleep 0.2`, 100, true},
		{"rounded", "seq 1 1234; sleep 0.2", 1234, true},
		{"interleaved", "printf a; sleep 0.05; printf b >&2; sleep 0.05; printf '\\nc'; sleep 0.2", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			legacyActivity := false
			adapted := tool.AdaptTool(bashTool(t), core.ToolSchema{Name: "Bash"}, func() string { return cwd }, tool.WithToolActivity(func(string, string) { legacyActivity = true }))
			input, _ := json.Marshal(map[string]any{"command": tc.command, "timeout_ms": 30000})
			driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "bash-1", Name: "Bash", Input: string(input)}), aitest.Says("done"))
			a, err := sdkagent.New(driver.Client(), sdkagent.WithTools(adapted))
			if err != nil {
				t.Fatal(err)
			}
			var latest toolresult.BashDetails
			var counter string
			var final toolresult.BashDetails
			for event, err := range a.Run(context.Background(), ai.UserMessage("run")) {
				if err != nil {
					t.Fatal(err)
				}
				switch e := event.(type) {
				case sdkagent.ToolUpdate:
					d, ok := e.Partial.Details.(toolresult.BashDetails)
					if !ok {
						t.Fatalf("untyped progress: %#v", e.Partial)
					}
					if len(d.Tail) > progressTailBytes {
						t.Fatalf("tail bytes=%d", len(d.Tail))
					}
					latest, counter = d, e.Partial.Text()
				case sdkagent.ToolEnd:
					if e.Err != nil {
						t.Fatal(e.Err)
					}
					final = e.Result.Details.(toolresult.BashDetails)
				case sdkagent.TurnEnd:
					encoded, _ := json.Marshal(a.Messages())
					if strings.Contains(string(encoded), "tailTruncated") || strings.Contains(string(encoded), "lineCount") {
						t.Fatal("partial metadata leaked into transcript")
					}
				}
			}
			if latest.LineCount != tc.rows || final.LineCount != tc.rows || latest.TailTruncated != tc.truncated || final.TailTruncated != tc.truncated {
				t.Fatalf("live=%+v final=%+v want rows=%d truncated=%v", latest, final, tc.rows, tc.truncated)
			}
			if counter == "" || legacyActivity {
				t.Fatalf("counter=%q legacy activity=%v", counter, legacyActivity)
			}
			if tc.name == "rounded" && !strings.Contains(counter, "1.2k") {
				t.Fatalf("counter not rounded: %q", counter)
			}
		})
	}
}
