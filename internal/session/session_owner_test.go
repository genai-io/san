package session

import "testing"

func TestSessionOwnedByOriginalStore(t *testing.T) {
	store := cleanStore(t)
	owner := createTyped(t, store, CreateOptions{ID: "owner"})
	sameProject, err := openStoreIn(store.projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sameProject.Close()
	other := cleanStore(t)
	otherOwner := createTyped(t, other, CreateOptions{ID: owner.ID()})
	if !owner.OwnedBy(store) || owner.OwnedBy(sameProject) || owner.OwnedBy(other) || otherOwner.OwnedBy(store) {
		t.Fatal("session ownership was inferred from shared project or session IDs")
	}
	if (*Session)(nil).OwnedBy(store) || owner.OwnedBy(nil) {
		t.Fatal("missing owner or store accepted")
	}
}
