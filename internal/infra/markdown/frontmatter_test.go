package markdown

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFrontmatterFile(t *testing.T) {
	for _, tc := range []struct {
		name, src, fm, body string
		wantErr             bool
	}{
		{name: "none", src: "# Title\n\ntext\n", body: "# Title\n\ntext"},
		{name: "frontmatter", src: "---\nname: x\n---\n\nbody\n", fm: "name: x\n", body: "body"},
		{name: "crlf", src: "--- \r\nname: x\r\n---\r\nbody\r\n", fm: "name: x\n", body: "body"},
		{name: "unclosed", src: "---\nname: x\n", wantErr: true},
	} {
		path := filepath.Join(t.TempDir(), "f.md")
		if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
			t.Fatal(err)
		}
		fm, body, err := ParseFrontmatterFile(path)
		if (err != nil) != tc.wantErr || fm != tc.fm || body != tc.body {
			t.Errorf("%s: got (%q, %q, %v)", tc.name, fm, body, err)
		}
	}
}
