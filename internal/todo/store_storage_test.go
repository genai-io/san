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

func TestReloadAfterDeletion(t *testing.T) {
	dir := t.TempDir()
	writer, reader := NewStore(), NewStore()
	if err := writer.SetStorageDir(dir); err != nil {
		t.Fatal(err)
	}
	gone := writer.Create("remove", "", "", nil).ID
	keep := writer.Create("keep", "", "", nil).ID
	if err := reader.SetStorageDir(dir); err != nil {
		t.Fatal(err)
	}
	reader.ReloadFromDisk()
	if err := writer.Delete(gone); err != nil {
		t.Fatal(err)
	}
	reader.ReloadFromDisk()
	if _, ok := reader.Get(gone); ok {
		t.Fatal("deleted item still visible")
	}
	if _, ok := reader.Get(keep); !ok {
		t.Fatal("surviving item disappeared")
	}
	if err := reader.Delete(keep); err != nil {
		t.Fatal(err)
	}
	reader.ReloadFromDisk()
	if items := reader.Export(); len(items) != 1 || items[0].Status != StatusDeleted {
		t.Fatal("no-op refresh lost local deletion")
	}
}

func TestRefreshKeepsUnpersistedItem(t *testing.T) {
	store := NewStore()
	dir := t.TempDir()
	if err := store.SetStorageDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "1.json"), 0755); err != nil {
		t.Fatal(err)
	}
	store.ReloadFromDisk()
	item := store.Create("unsaved", "", "", nil)
	store.ReloadFromDisk()
	if _, ok := store.Get(item.ID); !ok {
		t.Fatal("lost unsaved item")
	}
	// The same scan must distinguish a successfully written sibling that
	// disappeared from the item that never made it to disk.
	sibling := store.Create("saved", "", "", nil)
	if err := os.Remove(filepath.Join(dir, sibling.ID+".json")); err != nil {
		t.Fatal(err)
	}
	store.ReloadFromDisk()
	if _, ok := store.Get(sibling.ID); ok {
		t.Fatal("deleted sibling still visible")
	}
	if _, ok := store.Get(item.ID); !ok {
		t.Fatal("sibling deletion discarded unsaved item")
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
