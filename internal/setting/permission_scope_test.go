package setting

import (
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestScopedPermissions(t *testing.T) {
	cwd, outside := t.TempDir(), t.TempDir()
	session := NewSessionPermissions()
	session.AddWorkingDirectory(cwd)
	session.SetMode(ModeAutoAccept)
	for _, name := range []string{"Read", "Write", "Edit"} {
		for _, path := range []string{"result.txt", filepath.Join(cwd, "result.txt")} {
			for _, pattern := range []string{"result.txt", filepath.Join(cwd, "result.txt")} {
				// A relative deny matches relative model input; absolute denies also
				// match the resolved path without changing the original model arguments.
				if filepath.IsAbs(path) && !filepath.IsAbs(pattern) {
					continue
				}
				args := map[string]any{"file_path": path}
				data := &Data{}
				data.Permissions.Deny = []string{name + "(" + pattern + ")"}
				scoped := ScopedPermissions{Data: data, CWD: cwd}
				if got := scoped.HasPermissionToUseTool(name, args, session); got.Behavior != perm.Reject {
					t.Fatalf("%s %s %s: %+v", name, path, pattern, got)
				}
				if scoped.CanApproveByHook(name, args, session) {
					t.Fatal("hook bypassed deny")
				}
				if args["file_path"] != path {
					t.Fatal("model input mutated")
				}
			}
		}
	}
	args := map[string]any{"file_path": "result.txt"}
	for _, dir := range []string{cwd, outside} {
		scoped := ScopedPermissions{Data: &Data{}, CWD: dir}
		got := scoped.HasPermissionToUseTool("Write", args, session)
		want := perm.Permit
		if dir == outside {
			want = perm.Prompt
		}
		if got.Behavior != want {
			t.Fatalf("cwd %s: %+v", dir, got)
		}
		if dir == outside && scoped.CanApproveByHook("Write", args, session) {
			t.Fatal("hook bypassed outside confirmation")
		}
	}
}
