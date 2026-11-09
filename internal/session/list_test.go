package session

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

var listNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) int64 { return listNow.Add(-d).UnixMilli() }

// listSession creates a session whose last message was sent at lastActive.
func listSession(t *testing.T, s *Store, id string, lastActive int64, mutate func(*SessionCreatePayload)) {
	t.Helper()
	ctx := context.Background()
	payload := SessionCreatePayload{ProjectID: s.projectID, Workspace: s.workspace, InitialCWD: s.cwd, AgentID: "main"}
	if mutate != nil {
		mutate(&payload)
	}
	create := Event{Seq: 1, Type: EventSessionCreate, SchemaVersion: 1, Time: lastActive - 1, Payload: payload}
	var validation EventValidationContext
	if payload.Parent != nil {
		validation.ParentAncestors = map[string][]string{payload.Parent.Session: {payload.Parent.Session}}
	}
	opened, err := s.createSession(ctx, "c-"+id, id, create, validation)
	if err != nil {
		t.Fatal(err)
	}
	msg := Event{Seq: 2, Type: EventMessageAppend, SchemaVersion: 1, Time: lastActive,
		Payload: MessageAppendPayload{Messages: []ai.Message{eventText("m-"+id, ai.RoleUser, "prompt "+id)}}}
	commitTestEvents(t, opened, "m-"+id, msg)
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
}

func listIDs(t *testing.T, s *Store, q ListQuery) []string {
	t.Helper()
	q.Now = listNow
	var ids []string
	for {
		page, err := s.listOverviews(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			ids = append(ids, it.SessionID)
		}
		if page.Next == "" {
			return ids
		}
		q.Cursor = page.Next
	}
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListRecentWindowPinAndArchive(t *testing.T) {
	store, err := openStoreIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	day := 24 * time.Hour
	listSession(t, store, "new", ago(time.Hour), nil)
	listSession(t, store, "tie-a", ago(2*day), nil)
	listSession(t, store, "tie-b", ago(2*day), nil)
	listSession(t, store, "old", ago(60*day), nil)
	listSession(t, store, "old-pinned", ago(90*day), nil)
	listSession(t, store, "archived", ago(3*day), nil)
	if _, err := updateAttrs(store.sessionDir("old-pinned"), AttrsPatch{Pinned: SetField(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := updateAttrs(store.sessionDir("archived"), AttrsPatch{ArchivedAt: SetField(int64(1))}); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		q    ListQuery
		want []string
	}{
		"default":         {ListQuery{}, []string{"new", "tie-b", "tie-a", "old-pinned"}},
		"all ages":        {ListQuery{All: true}, []string{"new", "tie-b", "tie-a", "old", "old-pinned"}},
		"archived only":   {ListQuery{Archived: ArchivedOnly}, []string{"archived"}},
		"archived too":    {ListQuery{Archived: ArchivedInclude}, []string{"new", "tie-b", "tie-a", "archived", "old-pinned"}},
		"small pages":     {ListQuery{Limit: 1, All: true}, []string{"new", "tie-b", "tie-a", "old", "old-pinned"}},
		"pinned vs only":  {ListQuery{Archived: ArchivedOnly, All: true}, []string{"archived"}},
		"workspace match": {ListQuery{Workspace: store.workspace}, []string{"new", "tie-b", "tie-a", "old-pinned"}},
		"workspace other": {ListQuery{Workspace: "/elsewhere"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if got := listIDs(t, store, c.q); !equalIDs(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestListParentTrashAndDamage(t *testing.T) {
	ctx := context.Background()
	store, err := openStoreIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	listSession(t, store, "parent", ago(time.Hour), nil)
	listSession(t, store, "child", ago(2*time.Hour), func(p *SessionCreatePayload) { p.Parent = &ParentLink{Session: "parent"} })
	listSession(t, store, "gone", ago(90*24*time.Hour), nil)
	if got := listIDs(t, store, ListQuery{Parent: "parent"}); !equalIDs(got, []string{"child"}) {
		t.Fatalf("children: %v", got)
	}
	lib := library{root: store.projectDir}
	if err := lib.trash(ctx, "gone", listNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := listIDs(t, store, ListQuery{}); !equalIDs(got, []string{"parent", "child"}) {
		t.Fatalf("live hides trash: %v", got)
	}
	if got := listIDs(t, store, ListQuery{Visibility: VisibleTrash}); !equalIDs(got, []string{"gone"}) {
		t.Fatalf("trash lists without an age cut: %v", got)
	}
	page, _ := store.listOverviews(ctx, ListQuery{Visibility: VisibleTrash, Now: listNow})
	if tr := page.Items[0].Trashed; tr == nil || !tr.Deadline().Equal(listNow.Add(-time.Hour).Add(trashRetention)) {
		t.Fatalf("trash item lacks its deadline: %+v", page.Items[0])
	}
	if got := listIDs(t, store, ListQuery{Visibility: VisibleAll}); len(got) != 3 {
		t.Fatalf("all: %v", got)
	}
	// A session that cannot be read stays visible with its error.
	listSession(t, store, "broken", ago(time.Minute), nil)
	if err := os.WriteFile(store.eventsPath("broken"), []byte("{\"batch_version\":99}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err = store.listOverviews(ctx, ListQuery{Now: listNow})
	if err != nil {
		t.Fatal(err)
	}
	var broken *ListItem
	for i := range page.Items {
		if page.Items[i].SessionID == "broken" {
			broken = &page.Items[i]
		}
	}
	if broken == nil || broken.Err == nil {
		t.Fatalf("damaged session vanished or reads clean: %+v", page.Items)
	}
}

func TestListDisplayTitleAndBadInput(t *testing.T) {
	ctx := context.Background()
	store, err := openStoreIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	listSession(t, store, "a", ago(time.Hour), nil)
	if _, err := updateAttrs(store.sessionDir("a"), AttrsPatch{Title: SetField("Mine")}); err != nil {
		t.Fatal(err)
	}
	page, _ := store.listOverviews(ctx, ListQuery{Now: listNow})
	if page.Items[0].DisplayTitle() != "Mine" {
		t.Fatalf("title %q", page.Items[0].DisplayTitle())
	}
	for _, q := range []ListQuery{{Limit: -1}, {Limit: maxListLimit + 1}, {Visibility: "x"}, {Archived: "x"}, {Cursor: "!!"}} {
		if _, err := store.listOverviews(ctx, q); err == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
}
