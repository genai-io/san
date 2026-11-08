package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAttrsDefaultsPatchAndRemoval(t *testing.T) {
	dir := t.TempDir()
	if a, err := readAttrs(dir); err != nil || a != (Attrs{}) {
		t.Fatalf("missing file must mean defaults: %+v %v", a, err)
	}
	a, err := updateAttrs(dir, AttrsPatch{Title: SetField("Payments"), Pinned: SetField(true)})
	if err != nil || a.Title != "Payments" || !a.Pinned {
		t.Fatalf("%+v %v", a, err)
	}
	a, err = updateAttrs(dir, AttrsPatch{ArchivedAt: SetField(int64(5))})
	if err != nil || a.Title != "Payments" || a.ArchivedAt != 5 || !a.Pinned {
		t.Fatalf("absent fields must stay: %+v %v", a, err)
	}
	if _, err = updateAttrs(dir, AttrsPatch{Title: ClearField[string](), ArchivedAt: ClearField[int64](), Pinned: SetField(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, attrsFile)); !os.IsNotExist(err) {
		t.Fatal("all-default attrs must leave no file")
	}
	if _, err := updateAttrs(dir, AttrsPatch{ArchivedAt: SetField(int64(-1))}); err == nil {
		t.Fatal("accepted negative archive time")
	}
}

func TestAttrsDamageIsNotReset(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, attrsFile), []byte(`{"title":"x","extra":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAttrs(dir); err == nil {
		t.Fatal("accepted unknown field")
	}
	if _, err := updateAttrs(dir, AttrsPatch{Pinned: SetField(true)}); err == nil {
		t.Fatal("overwrote damaged user intent")
	}
}

func TestAttrsConcurrentUpdatesKeepAll(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for _, p := range []AttrsPatch{{Title: SetField("t")}, {Pinned: SetField(true)}, {ArchivedAt: SetField(int64(9))}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := updateAttrs(dir, p); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if a, err := readAttrs(dir); err != nil || a != (Attrs{Title: "t", ArchivedAt: 9, Pinned: true}) {
		t.Fatalf("lost update: %+v %v", a, err)
	}
}
