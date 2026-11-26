package confdir

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// Config folders linked in from elsewhere are walked like real ones, the
// root included, and a link cycle is walked once.
func TestWalkFilesFollowsSymlinks(t *testing.T) {
	tmp := t.TempDir()
	shared := filepath.Join(tmp, "shared", "jenkins")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "SKILL.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(tmp, "skills")
	if err := os.Mkdir(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, shared, filepath.Join(skills, "jenkins"))
	symlink(t, skills, filepath.Join(skills, "loop"))
	root := filepath.Join(tmp, "linked-root")
	symlink(t, skills, root)

	var got []string
	WalkFiles(root, func(path string) { got = append(got, path) })
	want := []string{filepath.Join(root, "jenkins", "SKILL.md")}
	if !slices.Equal(got, want) {
		t.Fatalf("WalkFiles = %v, want %v", got, want)
	}
}
