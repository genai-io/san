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

	if got := store.StorageDir(); got != validDir {
		t.Fatalf("storage dir = %q, want previous %q", got, validDir)
	}
	if _, ok := store.Get("1"); !ok {
		t.Fatal("failed storage reconfiguration discarded in-memory items")
	}
}

func TestExportOwnsTaskMetadataAndDependencies(t *testing.T) {
	store := NewStore()
	item := store.Create("work", "", "", map[string]any{"worker": "original"})
	item.Blocks = []string{"2"}
	item.BlockedBy = []string{"3"}
	exported := store.Export()
	exported[0].Metadata["worker"] = "changed"
	exported[0].Blocks[0] = "changed"
	exported[0].BlockedBy[0] = "changed"
	current, ok := store.Get(item.ID)
	if !ok || current.Metadata["worker"] != "original" || current.Blocks[0] != "2" || current.BlockedBy[0] != "3" {
		t.Fatalf("export mutated source: %+v", current)
	}
}
