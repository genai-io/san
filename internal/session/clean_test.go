package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

func cleanStore(t *testing.T) *Store {
	t.Helper()
	s, err := openStoreIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func isLive(s *Store, id string) bool {
	_, err := os.Stat(s.sessionDir(id))
	return err == nil
}

func TestCleanPurgeScopeAndSharedBudget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ids        []string
		retention  bool
		wantPurged []string
		truncated  bool
	}{
		{name: "selected subset", ids: []string{"b"}, wantPurged: []string{"b"}},
		{name: "physical deletion limit", wantPurged: []string{"a"}, truncated: true},
		{name: "retention shares budget", retention: true, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := cleanStore(t)
			lib := library{root: s.projectDir}
			for _, id := range []string{"a", "b", "c"} {
				listSession(t, s, id, ago(300*24*time.Hour), nil)
				if err := lib.trash(ctx, id, listNow.Add(-8*24*time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			opts := CleanOptions{Now: listNow, IDs: tc.ids, Limit: 1, DryRun: true}
			if tc.retention {
				listSession(t, s, "idle", ago(300*24*time.Hour), nil)
				opts.InactiveAfter = 90 * 24 * time.Hour
			}
			dry, err := s.clean(ctx, opts)
			if err != nil || !reflect.DeepEqual(dry.Purged, tc.wantPurged) || dry.Truncated != tc.truncated {
				t.Fatalf("preview: %+v, %v", dry, err)
			}
			for _, id := range []string{"a", "b", "c"} {
				if _, err := os.Stat(lib.trashDir(id)); err != nil {
					t.Fatalf("preview removed %s: %v", id, err)
				}
			}
			if tc.retention && !isLive(s, "idle") {
				t.Fatal("preview moved live session")
			}
			opts.DryRun = false
			actual, err := s.clean(ctx, opts)
			if err != nil || !reflect.DeepEqual(actual, dry) {
				t.Fatalf("actual: %+v, preview: %+v, error: %v", actual, dry, err)
			}
			for _, id := range []string{"a", "b", "c"} {
				_, err := os.Stat(lib.trashDir(id))
				wantRemoved := len(tc.wantPurged) > 0 && tc.wantPurged[0] == id
				if os.IsNotExist(err) != wantRemoved {
					t.Fatalf("%s removed=%v, want %v (err=%v)", id, os.IsNotExist(err), wantRemoved, err)
				}
			}
			if tc.retention && (!reflect.DeepEqual(actual.Trashed, []string{"idle"}) || isLive(s, "idle")) {
				t.Fatalf("retention: %+v", actual)
			}
		})
	}
}

func TestCleanBoundsInterruptedPurges(t *testing.T) {
	ctx := context.Background()
	s := cleanStore(t)
	lib := library{root: s.projectDir}
	for _, id := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(lib.trashRoot(), purgingPrefix+id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	opts := CleanOptions{Now: listNow, IDs: []string{"b", "c"}, Limit: 1, DryRun: true}
	dry, err := s.clean(ctx, opts)
	if err != nil || !reflect.DeepEqual(dry.Purged, []string{"b"}) || !dry.Truncated {
		t.Fatalf("preview: %+v, %v", dry, err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := os.Stat(filepath.Join(lib.trashRoot(), purgingPrefix+id)); err != nil {
			t.Fatal(err)
		}
	}
	opts.DryRun = false
	actual, err := s.clean(ctx, opts)
	if err != nil || !reflect.DeepEqual(actual, dry) {
		t.Fatalf("actual: %+v, preview: %+v, %v", actual, dry, err)
	}
	for _, id := range []string{"a", "b", "c"} {
		_, err := os.Stat(filepath.Join(lib.trashRoot(), purgingPrefix+id))
		if os.IsNotExist(err) != (id == "b") {
			t.Fatalf("unexpected removal of %s: %v", id, err)
		}
	}
}

func TestCleanRetentionIsOffUnlessAsked(t *testing.T) {
	ctx := context.Background()
	s := cleanStore(t)
	listSession(t, s, "ancient", ago(400*24*time.Hour), nil)
	res, err := s.clean(ctx, CleanOptions{Now: listNow})
	if err != nil || len(res.Trashed) != 0 || !isLive(s, "ancient") {
		t.Fatalf("retention ran by default: %+v %v", res, err)
	}
}

func TestCleanRetentionMovesIdleSessionsAndProtectsTheRest(t *testing.T) {
	ctx := context.Background()
	s := cleanStore(t)
	day := 24 * time.Hour
	listSession(t, s, "idle-old", ago(200*day), nil)
	listSession(t, s, "idle-older", ago(300*day), nil)
	listSession(t, s, "fresh", ago(10*day), nil)
	listSession(t, s, "pinned", ago(250*day), nil)
	listSession(t, s, "busy", ago(260*day), nil)
	if _, err := updateAttrs(s.sessionDir("pinned"), AttrsPatch{Pinned: SetField(true)}); err != nil {
		t.Fatal(err)
	}
	forkSource(t, s) // "src": ancient, with an unfinished model call
	open, err := s.openSession(ctx, "busy")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	opts := CleanOptions{InactiveAfter: 90 * day, Workspace: s.workspace, Now: listNow}

	preview := opts
	preview.DryRun = true
	dry, err := s.clean(ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	// A preview claims no lease, so it cannot see a session another process has open.
	if !reflect.DeepEqual(dry.Trashed, []string{"idle-older", "busy", "idle-old"}) || !isLive(s, "idle-old") {
		t.Fatalf("dry run: %+v", dry)
	}
	res, err := s.clean(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Trashed, []string{"idle-older", "idle-old"}) {
		t.Fatalf("the run moves only what it can still claim: %+v", res)
	}
	for _, id := range []string{"idle-old", "idle-older"} {
		if isLive(s, id) {
			t.Fatalf("%s still live", id)
		}
	}
	reasons := map[string]string{}
	for _, sk := range res.Skipped {
		reasons[sk.ID] = sk.Reason
	}
	want := map[string]string{"pinned": skipPinned, "busy": skipBusy, "src": skipUnfinished}
	if !reflect.DeepEqual(reasons, want) {
		t.Fatalf("skips: %v, want %v", reasons, want)
	}
	for _, id := range []string{"fresh", "pinned", "busy", "src"} {
		if !isLive(s, id) {
			t.Fatalf("%s was moved", id)
		}
	}
}

func TestCleanRetentionStaysInTheAuthorisedWorkspace(t *testing.T) {
	s := cleanStore(t)
	listSession(t, s, "old", ago(400*24*time.Hour), nil)
	res, err := s.clean(context.Background(), CleanOptions{InactiveAfter: 90 * 24 * time.Hour, Workspace: "/some/other/workspace", Now: listNow})
	if err != nil || len(res.Trashed) != 0 || len(res.Skipped) != 0 || !isLive(s, "old") {
		t.Fatalf("retention left its workspace: %+v %v", res, err)
	}
}

func TestCleanLimitAndIDsNarrow(t *testing.T) {
	ctx := context.Background()
	s := cleanStore(t)
	day := 24 * time.Hour
	for i, id := range []string{"a", "b", "c"} {
		listSession(t, s, id, ago(time.Duration(300-i)*day), nil)
	}
	res, err := s.clean(ctx, CleanOptions{InactiveAfter: 90 * day, Limit: 2, Now: listNow})
	if err != nil || !reflect.DeepEqual(res.Trashed, []string{"a", "b"}) || !res.Truncated || !isLive(s, "c") {
		t.Fatalf("limit: %+v %v", res, err)
	}
	res, err = s.clean(ctx, CleanOptions{InactiveAfter: 90 * day, IDs: []string{"nope"}, Now: listNow})
	if err != nil || len(res.Trashed) != 0 || !isLive(s, "c") {
		t.Fatalf("ids must only narrow: %+v %v", res, err)
	}
	for _, bad := range []CleanOptions{{Limit: -1}, {Limit: maxCleanLimit + 1}, {InactiveAfter: -1}} {
		if _, err := s.clean(ctx, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestTrashGuardCancelsAStaleChoice(t *testing.T) {
	ctx := context.Background()
	s := cleanStore(t)
	listSession(t, s, "used", ago(time.Hour), nil)
	lib := library{root: s.projectDir}
	err := lib.trashChecked(ctx, "used", listNow, func(string) error { return errors.New(skipChanged) })
	if err == nil || !isLive(s, "used") {
		t.Fatalf("guard did not stop the move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.sessionDir("used"), deletedFile)); !os.IsNotExist(err) {
		t.Fatal("a cancelled move left a marker")
	}
	if err := lib.trashChecked(ctx, "used", listNow, nil); err != nil {
		t.Fatal(err)
	}
	if got := trashSkipReason(sdksession.ErrBusy); got != skipBusy {
		t.Fatal(got)
	}
}

func TestCleanPurgesExpiredTrashAndCollectsGarbage(t *testing.T) {
	ctx := context.Background()
	s := cleanStore(t)
	lib := library{root: s.projectDir}
	listSession(t, s, "expired", ago(time.Hour), nil)
	listSession(t, s, "recent", ago(time.Hour), nil)
	if err := lib.trash(ctx, "expired", listNow.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := lib.trash(ctx, "recent", listNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	forkSource(t, s)
	live := s.sessionDir("src")
	blobs := filepath.Join(live, "blobs")
	stale := filepath.Join(blobs, blobDigest([]byte("unreferenced")))
	fresh := filepath.Join(blobs, blobDigest([]byte("fresh")))
	unknown := filepath.Join(blobs, "notes.txt")
	staging := filepath.Join(s.sessionsRoot(), ".half-made")
	orphan := filepath.Join(s.projectDir, "cache", "gone.json")
	for _, p := range []string{stale, fresh, unknown, orphan} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	longAgo := listNow.Add(-48 * time.Hour)
	for _, p := range []string{stale, unknown, staging, orphan} {
		if err := os.Chtimes(p, longAgo, longAgo); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(fresh, listNow, listNow); err != nil {
		t.Fatal(err)
	}

	dry, err := s.clean(ctx, CleanOptions{DryRun: true, Now: listNow})
	if err != nil || !reflect.DeepEqual(dry.Purged, []string{"expired"}) || len(dry.Removed) != 3 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	for _, p := range []string{stale, staging, orphan, lib.trashDir("expired")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("dry run removed %s", p)
		}
	}
	res, err := s.clean(ctx, CleanOptions{Now: listNow})
	if err != nil || !reflect.DeepEqual(res.Purged, []string{"expired"}) || len(res.Removed) != 3 {
		t.Fatalf("clean: %+v %v", res, err)
	}
	for _, p := range []string{stale, staging, orphan, lib.trashDir("expired")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s survived", p)
		}
	}
	// What is referenced, fresh, unknown or still in its deadline is never touched.
	referenced := forkReferencedBlob(t, s)
	for _, p := range []string{fresh, unknown, referenced, lib.trashDir("recent")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was removed: %v", p, err)
		}
	}
}

func forkReferencedBlob(t *testing.T, s *Store) string {
	t.Helper()
	state, err := s.readEvents(context.Background(), "src", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range state.projection.data.Events {
		if in, ok := e.Payload.(*InferenceStartPayload); ok && in.Input.Standalone != "" {
			return filepath.Join(s.sessionDir("src"), "blobs", in.Input.Standalone)
		}
	}
	t.Fatal("no referenced blob")
	return ""
}
