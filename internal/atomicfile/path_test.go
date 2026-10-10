package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFilePreservesBackslashInDirectoryName(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("backslash is a path separator")
	}
	dir := t.TempDir()
	parent := filepath.Join(dir, `project\`)
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "new.txt")
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "new" {
		t.Fatalf("requested target = %q, err = %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "project")); !os.IsNotExist(err) {
		t.Fatalf("unrequested sibling directory exists: %v", err)
	}
}

func TestResolvePathWithMissingParentsAndSymlinkTargets(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("target", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	missing := filepath.Join(link, "new-dir", "new-file")
	got, err := ResolvePath(missing)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(want, "new-dir", "new-file") {
		t.Fatalf("resolved = %q", got)
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join("target", "new-dir", "new-file"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dangling, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(missing); err != nil || string(got) != "new" {
		t.Fatalf("target = %q, err = %v", got, err)
	}
	if info, err := os.Lstat(dangling); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v, %v", info, err)
	}
	if err := os.Symlink("loop", filepath.Join(dir, "loop")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolvePath(filepath.Join(dir, "loop")); err == nil {
		t.Fatal("symlink loop accepted")
	}
}

func TestWriteFileResolvesSymlinksBeforeParentTraversal(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "target", "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(inner, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	target := filepath.Join(dir, "target", "file")
	facade := filepath.Join(dir, "file")
	for _, path := range []string{target, facade} {
		if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := link + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "file"
	if err := WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "replacement" {
		t.Fatalf("actual target = %q, %v", got, err)
	}
	if got, err := os.ReadFile(facade); err != nil || string(got) != "original" {
		t.Fatalf("unseen file changed: %q, %v", got, err)
	}
	newPath := link + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "new-file"
	if err := WriteFile(newPath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "target", "new-file")); err != nil || string(got) != "new" {
		t.Fatalf("new target = %q, %v", got, err)
	}
	// A dangling link with an absent intermediate directory must not turn an
	// apparently new file into an overwrite of a different existing file.
	dangling := filepath.Join(dir, "dangling-parent")
	if err := os.Symlink("target/missing/../file", dangling); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dangling, []byte("unseen"), 0o600); err == nil {
		t.Fatal("unseen existing target overwritten")
	}
}
