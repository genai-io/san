package setting

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoaderCanonicalizesCheckoutBeforeRootDiscovery(t *testing.T) {
	trustHome(t)
	_, wt := configWorktree(t)
	sub := filepath.Join(wt, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(sub, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(base)
	for _, cwd := range []string{sub, alias, "alias"} {
		loader := NewLoaderForCwd(cwd)
		if loader.projectDir != filepath.Join(canonical, ".san") {
			t.Errorf("loader(%q) projectDir=%q want checkout %q", cwd, loader.projectDir, canonical)
		}
	}
}
