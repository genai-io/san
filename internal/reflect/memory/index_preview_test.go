package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
)

func TestIndexPreviewLimits(t *testing.T) {
	shortLines := strings.Repeat("topic\n", 200)
	byteLimit := strings.Repeat("x", 24_998) + "\r\n"
	beforeUTF8 := strings.Repeat("x", 24_997) + "\n"
	for _, tt := range []struct {
		name, text, want string
		next             int
	}{
		{"short file without final newline", "one\ntwo", "one\ntwo", 0},
		{"exact line limit", shortLines, shortLines, 0},
		{"line limit before byte limit", shortLines + "tail", shortLines, 201},
		{"exact byte limit", byteLimit, byteLimit, 0},
		{"byte limit before line limit", byteLimit + "tail", byteLimit, 2},
		{"byte limit within UTF-8 line", beforeUTF8 + "记忆\n", beforeUTF8, 2},
		{"first line exceeds byte limit", strings.Repeat("x", 25_001), "", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, next := indexPreview(tt.text, setting.MemoryPreviewLimits{})
			if got != tt.want || next != tt.next {
				t.Fatalf("got %d bytes and next line %d, want %d bytes and next line %d", len(got), next, len(tt.want), tt.next)
			}
		})
	}
}

func TestIndexLookupsDoNotChangeIndex(t *testing.T) {
	store := system.MemoryStore(t.TempDir())
	limits := setting.MemoryPreviewLimits{Lines: 1, KB: 2}
	path := filepath.Join(string(store), system.AutoMemoryIndexName)
	const prefix = "- [One](one.md) — indexed note\n"
	const tail = "- [Release](tail.md) — secret release note\n"
	const text = prefix + tail + "```\n- [Literal](literal.md) — release\n```\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	index := ReadIndex(store, limits)
	if got, more := index.Preview(); got != prefix || !more || !strings.Contains(index.Hint().Text, `"line":2`) {
		t.Fatal("preview did not use the configured line limit")
	}
	for n, req := range []IndexRequest{{Action: actionSearch, Query: "RELEASE secret"}, {Action: actionIndexRead, Line: 2}} {
		got, ids, err := index.Lookup(req)
		if err != nil || !strings.Contains(got, tail) || len(ids) != 1 || ids[0] != "tail.md" {
			t.Fatalf("%s: %q, %v, %v", req.Action, got, ids, err)
		}
		want := []string{"Lookups remaining: 1.", "Lookups remaining: 0."}[n]
		if header := index.Result(got).Content[0].Text; !strings.Contains(header, want) {
			t.Fatalf("lookup header = %q; want %q", header, want)
		}
	}
	if _, _, err := index.Lookup(IndexRequest{Action: actionIndexRead, Line: 2}); err == nil {
		t.Fatal("accepted a third lookup")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != text {
		t.Fatalf("lookup changed the index: %v", err)
	}
}
