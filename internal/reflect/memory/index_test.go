package memory

import "testing"

func TestUpdateIndexRefreshesEntry(t *testing.T) {
	const before = "# Memory\n\n" +
		"```markdown\n- [Example](topic.md)\n```\n\n" +
		"<!--\r\n- [Comment](topic.md)\r\n-->\r\n\r\n"
	p := proposal{Action: actionUpdate, File: "topic.md", Name: "New title", Type: "feedback", Description: "New summary"}
	for _, tc := range []struct {
		old, refreshed, after string
	}{
		{"+ [Old](./topic.md) — (project) Old summary\r\n", "+ [New title](./topic.md) — (feedback) New summary\r\n", "- [Other](other.md) — keep  \r\n"},
		{"* [Old](topic.md)", "* [New title](topic.md) — (feedback) New summary", ""},
	} {
		got, err := editIndex(before+tc.old+tc.after, p)
		want := before + tc.refreshed + tc.after
		if err != nil || got != want {
			t.Fatalf("editIndex = %q, %v; want %q", got, err, want)
		}
	}
}

func TestUpdateIndexRefusesUnownedTarget(t *testing.T) {
	const entry = "- [Old](topic.md)\n"
	p := proposal{Action: actionUpdate, File: "topic.md", Name: "New title", Type: "feedback", Description: "New summary"}
	for _, index := range []string{
		"- [Other](other.md)\n",
		"```\n" + entry + "```\n",
		entry + "* [Duplicate](./topic.md)\n",
		entry + "- [Alias](Topic.md)\n",
		"See [Old](topic.md) for details.\n" + entry,
		"- [Old](topic.md) and [Other](other.md)\n",
		"- [ ](topic.md)\n",
	} {
		got, err := editIndex(index, p)
		if err == nil || got != "" {
			t.Errorf("editIndex(%q) = %q, %v; want refusal", index, got, err)
		}
	}
}

func TestForgetIndexPreservesFencedExamples(t *testing.T) {
	const before = "# Memory\r\n\r\n" +
		"````markdown\r\n```\r\n- [Example](topic.md)\r\n````\r\n\r\n" +
		"   ~~~ markdown\r\n- [Another example](topic.md)\r\n   ~~~\r\n\r\n"
	const after = "- [Other](other.md) — keep  \r\n\r\n```\r\n- [Unclosed example](topic.md)"
	got, err := editIndex(before+"- [Topic](./topic.md) — remove\r\n"+after, proposal{Action: actionForget, File: "topic.md"})
	if err != nil || got != before+after {
		t.Fatalf("editIndex = %q, %v; want %q", got, err, before+after)
	}
}

func TestRememberAppendsAfterClosedLiteralBlock(t *testing.T) {
	for _, index := range []string{"```\r\n- [Example](topic.md)\r\n```", "<!--\r\n- [Example](topic.md)\r\n-->"} {
		got, err := editIndex(index, proposal{Action: actionRemember, File: "topic.md", Name: "Topic", Type: "feedback", Description: "Details"})
		want := index + "\r\n- [Topic](topic.md) — (feedback) Details\r\n"
		if err != nil || got != want {
			t.Fatalf("editIndex = %q, %v; want %q", got, err, want)
		}
	}
}

func TestForgetIndexRefusesCaseAmbiguity(t *testing.T) {
	for _, index := range []string{
		"- [Uppercase](Topic.md)\n",
		"- [Lowercase](topic.md)\n- [Uppercase](Topic.md)\n",
	} {
		// The index alone can't tell whether Topic.md is a distinct note
		// or another spelling of topic.md on a case-insensitive filesystem.
		got, err := editIndex(index, proposal{Action: actionForget, File: "topic.md"})
		if err == nil || got != "" {
			t.Fatalf("editIndex(%q) = %q, %v; want refusal", index, got, err)
		}
	}
}
