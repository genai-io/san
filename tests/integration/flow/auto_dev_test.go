package flow_test

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/task"
)

func TestAutoDev(t *testing.T) {
	data, err := os.ReadFile("../../../examples/flows/auto-dev.md")
	if err != nil {
		t.Fatal(err)
	}
	definition := string(data)
	b, err := flow.Parse(definition)
	if err != nil {
		t.Fatal(err)
	}
	if b.MaxParallel != 1 || len(b.Nodes) != 4 {
		t.Fatalf("want four serial nodes, got %+v", b)
	}
	preview := b.CompactProgressView(nil, 40)
	for _, line := range strings.Split(preview, "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Fatalf("preview exceeds width: %q", line)
		}
	}
	for _, label := range []string{"audit", "edit", "gain", "next"} {
		if !strings.Contains(preview, label) {
			t.Fatalf("missing %s in %s", label, preview)
		}
	}
	for _, tc := range []struct{ audit, edit, verdict, next string }{
		{"candidate-fixture-124", "measured-result-123", "keep", "continue"},
		{"no-fixture-456", "no-change-456", "skip", "pause"},
	} {
		t.Run(tc.verdict, func(t *testing.T) {
			final := tc.next + "\n" + tc.verdict
			p := &scriptedProvider{replies: []reply{{"Audit one candidate", tc.audit}, {"Test one hypothesis", tc.edit}, {"Decide the outcome", tc.verdict}, {"Decide whether", final}}}
			info := run(t, p, map[string]any{"definition": definition, "inputs": map[string]any{"scope": "example/scoped-package", "run": "prepared/example-run.md"}})
			if info.Status != task.StatusCompleted || info.Output != final {
				t.Fatalf("status=%s output=%q error=%s", info.Status, info.Output, info.Error)
			}
			turns := p.prompts()
			if len(turns) != 4 || !strings.HasSuffix(turns[3], "Gain verdict:\n"+tc.verdict) || strings.Contains(turns[3], tc.edit) || !strings.HasPrefix(turns[0], "Audit one candidate in example/scoped-package") || !strings.Contains(turns[1], tc.audit) || !strings.Contains(turns[2], tc.edit) || strings.Contains(turns[2], tc.audit) {
				t.Fatalf("unexpected handoffs: %q", turns)
			}
			for i, files := range [][]string{{"audit/SKILL.md", "audit/references/engineering.md", "audit/references/context.md"}, {"auto-dev/SKILL.md"}, {"gain/SKILL.md"}} {
				for _, file := range files {
					data, err := os.ReadFile("../../../.san/skills/" + file)
					if err != nil {
						t.Fatal(err)
					}
					body := strings.TrimSpace(string(data))
					if strings.HasPrefix(body, "---\n") {
						_, body, _ = strings.Cut(body, "\n---\n")
						body = strings.TrimSpace(body)
					}
					for j, prompt := range turns {
						if body == "" || strings.Contains(prompt, body) != (i == j) {
							t.Fatalf("%s body in wrong context or missing in turn %d", file, j)
						}
					}
				}
			}
			nativeChildren(t, info, 4)
			if strings.Contains(info.LiveView, tc.edit) {
				t.Fatal("worker handoff leaked into graph")
			}
		})
	}
}
