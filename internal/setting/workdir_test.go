package setting

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWorkingDirectoryRejectsSymlinkParentTraversal(t *testing.T) {
	cwd, outside := t.TempDir(), t.TempDir()
	inner := filepath.Join(outside, "inner")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cwd, "link")
	if err := os.Symlink(inner, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := link + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "new-file"
	if isInWorkingDirectory(path, []string{cwd}) {
		t.Fatal("symlink traversal outside cwd was permitted")
	}
}

func Test_isInWorkingDirectory(t *testing.T) {
	tests := []struct {
		name        string
		filePath    string
		workingDirs []string
		want        bool
	}{
		// No constraints — always allowed
		{"no working dirs", "/any/path/file.go", nil, true},
		{"empty working dirs", "/any/path/file.go", []string{}, true},

		// Path within working directory
		{"file in cwd", "/home/user/project/src/main.go", []string{"/home/user/project"}, true},
		{"file in cwd root", "/home/user/project/main.go", []string{"/home/user/project"}, true},
		{"deeply nested", "/home/user/project/a/b/c/d.go", []string{"/home/user/project"}, true},

		// Path IS the working directory
		{"exact match dir", "/home/user/project", []string{"/home/user/project"}, true},

		// Path outside working directory
		{"outside cwd", "/etc/passwd", []string{"/home/user/project"}, false},
		{"sibling dir", "/home/user/other/file.go", []string{"/home/user/project"}, false},
		{"parent dir", "/home/user/file.go", []string{"/home/user/project"}, false},

		// Traversal attempts
		{"dot-dot traversal", "/home/user/project/../other/file.go", []string{"/home/user/project"}, false},

		// Multiple working directories
		{"in second dir", "/tmp/scratch/file.go", []string{"/home/user/project", "/tmp/scratch"}, true},
		{"in neither dir", "/etc/config", []string{"/home/user/project", "/tmp/scratch"}, false},

		// Trailing slashes
		{"trailing slash dir", "/home/user/project/file.go", []string{"/home/user/project/"}, true},
		{"trailing slash path", "/home/user/project/file.go", []string{"/home/user/project"}, true},

		// Prefix attack (project-evil should NOT match project)
		{"prefix attack", "/home/user/project-evil/file.go", []string{"/home/user/project"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInWorkingDirectory(tt.filePath, tt.workingDirs)
			if got != tt.want {
				t.Errorf("isInWorkingDirectory(%q, %v) = %v, want %v", tt.filePath, tt.workingDirs, got, tt.want)
			}
		})
	}
}

func TestNormalizeMacOSPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific test")
	}

	tests := []struct {
		input string
		want  string
	}{
		{"/private/var/folders/abc", "/var/folders/abc"},
		{"/private/tmp/test", "/tmp/test"},
		{"/private/var", "/var"},
		{"/private/tmp", "/tmp"},
		{"/home/user/project", "/home/user/project"},
		{"/var/data", "/var/data"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeMacOSPath(tt.input)
			if got != tt.want {
				t.Errorf("normalizeMacOSPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsSubpath(t *testing.T) {
	tests := []struct {
		name   string
		child  string
		parent string
		want   bool
	}{
		{"inside", "/a/b/c", "/a/b", true},
		{"exact", "/a/b", "/a/b", true},
		{"outside", "/a/c", "/a/b", false},
		{"prefix attack", "/a/b-evil", "/a/b", false},
		{"parent", "/a", "/a/b", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSubpath(tt.child, tt.parent)
			if got != tt.want {
				t.Errorf("isSubpath(%q, %q) = %v, want %v", tt.child, tt.parent, got, tt.want)
			}
		})
	}
}
