package todo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetStorageDirRollsBackAfterConfigurationFailure(t *testing.T) {
	store := NewStore()
	validDir := t.TempDir()
	if err := store.SetStorageDir(validDir); err != nil {
		t.Fatalf("SetStorageDir(valid): %v", err)
	}
	store.Create("Keep me", "", "", nil)

	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("file"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := store.SetStorageDir(filepath.Join(parentFile, "child")); err == nil {
		t.Fatal("SetStorageDir(invalid) succeeded")
	}

	if got := store.GetStorageDir(); got != validDir {
		t.Fatalf("storage dir = %q, want previous %q", got, validDir)
	}
	if _, ok := store.Get("1"); !ok {
		t.Fatal("failed storage reconfiguration discarded in-memory items")
	}
}
