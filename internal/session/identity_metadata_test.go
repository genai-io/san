package session

import "testing"

func TestCreateWorkspaceMetadataRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLegacyStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(LegacyCreateOptions{ID: "identity", Workspace: "/worktrees/topic", Cwd: "/worktrees/topic/sub"})
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewLegacyStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.Read("identity", LegacyReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if history.Metadata.ProjectID != ProjectID(dir) || history.Metadata.Workspace != "/worktrees/topic" || history.Metadata.Cwd != "/worktrees/topic/sub" {
		t.Fatalf("metadata: %+v", history.Metadata)
	}
	for range 2 {
		summaries, err := store.List()
		if err != nil {
			t.Fatal(err)
		}
		if len(summaries) != 1 || summaries[0].ProjectID != history.Metadata.ProjectID || summaries[0].Workspace != history.Metadata.Workspace {
			t.Fatalf("summaries: %+v", summaries)
		}
	}
}
