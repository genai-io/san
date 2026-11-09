package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

var t0 = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func lifecycleFixture(t *testing.T) (library, *Store) {
	t.Helper()
	store, s, _ := eventStorageFixture(t)
	commitTestEvents(t, s, "note", testEvent(2, "", AnnotationAddPayload{Text: "kept"}))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return library{root: store.projectDir}, store
}

func TestTrashAndRestoreMoveTheWholeSession(t *testing.T) {
	ctx := context.Background()
	lib, store := lifecycleFixture(t)
	if _, err := updateAttrs(lib.sessionDir("session"), AttrsPatch{Title: SetField("Payments")}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.overview(ctx, "session"); err != nil {
		t.Fatal(err)
	}
	if err := lib.trash(ctx, "session", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lib.sessionDir("session")); !os.IsNotExist(err) {
		t.Fatal("trashed session is still live")
	}
	if _, err := os.Stat(lib.cacheFile("session")); !os.IsNotExist(err) {
		t.Fatal("cache survived the trash")
	}
	for _, f := range []string{eventsFileName, attrsFile, deletedFile} {
		if _, err := os.Stat(filepath.Join(lib.trashDir("session"), f)); err != nil {
			t.Fatalf("trash lacks %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(lib.trashDir("session"), eventsFileName+".lock")); !os.IsNotExist(err) {
		t.Fatal("stale writer lock left in the trash")
	}
	if _, err := store.openSession(ctx, "session"); err == nil {
		t.Fatal("trashed session opened")
	}
	list, err := lib.trashed(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "session" || !list[0].DeletedAt.Equal(t0) || !list[0].Deadline().Equal(t0.Add(trashRetention)) {
		t.Fatalf("trashed: %+v %v", list, err)
	}
	if err := lib.restore(ctx, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib.sessionDir("session"), deletedFile)); !os.IsNotExist(err) {
		t.Fatal("restore left the trash marker")
	}
	a, _ := readAttrs(lib.sessionDir("session"))
	opened, err := store.openSession(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if p, _ := opened.eventSnapshot(); a.Title != "Payments" || len(p.Events()) != 2 {
		t.Fatal("restore lost attrs or events")
	}
}

func TestTrashRefusesBusyPinnedAndUnknown(t *testing.T) {
	ctx := context.Background()
	lib, store := lifecycleFixture(t)
	open, err := store.openSession(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.trash(ctx, "session", t0); !errors.Is(err, sdksession.ErrBusy) {
		t.Fatalf("open session trashed: %v", err)
	}
	_ = open.Close()
	if _, err := updateAttrs(lib.sessionDir("session"), AttrsPatch{Pinned: SetField(true)}); err != nil {
		t.Fatal(err)
	}
	if err := lib.trash(ctx, "session", t0); !errors.Is(err, ErrPinned) {
		t.Fatalf("pinned session trashed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib.sessionDir("session"), deletedFile)); !os.IsNotExist(err) {
		t.Fatal("refusal left a marker behind")
	}
	if err := lib.trash(ctx, "nope", t0); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown session: %v", err)
	}
	if _, err := updateAttrs(lib.sessionDir("session"), AttrsPatch{Pinned: ClearField[bool]()}); err != nil {
		t.Fatal(err)
	}
	if err := lib.trash(ctx, "session", t0); err != nil {
		t.Fatalf("unpinned session: %v", err)
	}
}

func TestRestoreNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	lib, store := lifecycleFixture(t)
	if err := lib.trash(ctx, "session", t0); err != nil {
		t.Fatal(err)
	}
	create := testEvent(1, "", SessionCreatePayload{ProjectID: store.projectID, Workspace: store.workspace, InitialCWD: store.cwd, AgentID: "other"})
	again, err := store.createSession(ctx, "again", "session", create, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
	if err := lib.restore(ctx, "session"); !errors.Is(err, ErrExists) {
		t.Fatalf("restore over a live session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib.trashDir("session"), eventsFileName)); err != nil {
		t.Fatal("failed restore changed the trash")
	}
	if err := lib.restore(ctx, "missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore of nothing: %v", err)
	}
	// Trashing the live one would collide with the trash entry too.
	if err := lib.trash(ctx, "session", t0); !errors.Is(err, ErrExists) {
		t.Fatalf("trash over a trash entry: %v", err)
	}
}

func TestPurgeHonoursDeadlineDryRunAndUnknownAge(t *testing.T) {
	ctx := context.Background()
	lib, _ := lifecycleFixture(t)
	if err := lib.trash(ctx, "session", t0); err != nil {
		t.Fatal(err)
	}
	early := t0.Add(trashRetention - time.Second)
	if got, err := lib.purge(ctx, early, false); err != nil || len(got) != 0 {
		t.Fatalf("purged before the deadline: %v %v", got, err)
	}
	due := t0.Add(trashRetention)
	if got, err := lib.purge(ctx, due, true); err != nil || len(got) != 1 {
		t.Fatalf("dry run: %v %v", got, err)
	}
	if _, err := os.Stat(lib.trashDir("session")); err != nil {
		t.Fatal("dry run removed the session")
	}
	// An unreadable marker means unknown age: keep it however old the clock.
	if err := os.WriteFile(filepath.Join(lib.trashDir("session"), deletedFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.purge(ctx, due.Add(1000*time.Hour), false); len(got) != 0 {
		t.Fatal("purged a session of unknown age")
	}
	data := `{"deleted_at":` + strconv.FormatInt(t0.UnixMilli(), 10) + `}`
	if err := os.WriteFile(filepath.Join(lib.trashDir("session"), deletedFile), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	// An interrupted purge is finished by the next run.
	left := filepath.Join(lib.trashRoot(), purgingPrefix+"old")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := lib.purge(ctx, due, false)
	if err != nil || len(got) != 1 || got[0] != "session" {
		t.Fatalf("purge: %v %v", got, err)
	}
	entries, _ := os.ReadDir(lib.trashRoot())
	if len(entries) != 0 {
		t.Fatalf("trash not empty: %v", entries)
	}
}
