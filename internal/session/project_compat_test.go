package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectVersionOneKeepsIDWithoutMainConfig(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	projectTestGit(t, root, "init")
	projectTestGit(t, root, "commit", "--allow-empty", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	projectTestGit(t, root, "worktree", "add", "-b", "linked", linked)
	p := projectTestResolve(t, home, root)
	saved := projectRegistration{Version: 1, ID: p.ID, Kind: p.Kind, Key: p.Key, ConfigRoot: filepath.Join(t.TempDir(), "missing-original")}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Root, "project.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if q := projectTestResolve(t, home, linked); q.ID != p.ID {
		t.Fatal("version-1 ID replaced")
	}
}

func TestProjectChangedCommonDirectoryAtAnchorRejected(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	projectTestGit(t, root, "init")
	p := projectTestResolve(t, home, root)
	moved := filepath.Join(t.TempDir(), "metadata")
	if err := os.Rename(filepath.Join(root, ".git"), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+moved+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveProject(context.Background(), home, root); err == nil {
		t.Fatalf("accepted changed common dir for %s", p.ID)
	}
}

func TestProjectChangedKindAtAnchorRejected(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	projectTestResolve(t, home, root)
	projectTestGit(t, root, "init")
	if _, err := resolveProject(context.Background(), home, root); err == nil {
		t.Fatal("accepted directory becoming Git")
	}
}
