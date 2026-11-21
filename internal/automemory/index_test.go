package automemory

import "testing"

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
