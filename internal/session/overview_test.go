package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func overviewFixture(t *testing.T) (*Store, *eventSession) {
	t.Helper()
	store, s, _ := eventStorageFixture(t)
	commitTestEvents(t, s, "m1", testEvent(2, "", MessageAppendPayload{Messages: []ai.Message{eventText("u1", ai.RoleUser, "  first question  ")}}))
	return store, s
}

func TestOverviewProjectsListRow(t *testing.T) {
	store, s := overviewFixture(t)
	commitTestEvents(t, s, "t", testEvent(3, "", SessionUpdatePayload{State: &StatePatch{Title: SetField("Fix login")}, Config: &ConfigPatch{Model: SetField("m1")}}))
	note := testEvent(4, "", AnnotationAddPayload{Text: "note"})
	note.Time = 999999
	commitTestEvents(t, s, "n", note)
	got, err := store.overview(context.Background(), s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Fix login" || got.Model != "m1" || got.Messages != 1 || got.LastPrompt != "first question" || got.AgentID != "main" {
		t.Fatalf("%+v", got)
	}
	if got.LastActive != 123 || got.CreatedAt != 123 {
		t.Fatalf("an annotation must not count as activity: %+v", got)
	}
	long := strings.Repeat("é", 500)
	commitTestEvents(t, s, "long", testEvent(5, "", MessageAppendPayload{Messages: []ai.Message{eventText("u2", ai.RoleUser, long)}}))
	got, _ = store.overview(context.Background(), s.ID())
	if n := len([]rune(got.LastPrompt)); n != lastPromptRunes {
		t.Fatalf("last prompt runes = %d", n)
	}
}

func TestOverviewCacheHitMissAndRebuild(t *testing.T) {
	ctx := context.Background()
	store, s := overviewFixture(t)
	cacheFile := filepath.Join(store.projectDir, "cache", s.ID()+".json")
	first, err := store.overview(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatal("overview was not cached")
	}
	// A hit must not replay: break the events' product validity but not their
	// framing by checking the cache answers from its own file.
	data, _ := os.ReadFile(cacheFile)
	marked := strings.Replace(string(data), `"messages":1`, `"messages":41`, 1)
	if marked == string(data) {
		t.Fatal("fixture")
	}
	if err := os.WriteFile(cacheFile, []byte(marked), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.overview(ctx, s.ID()); got.Messages != 41 {
		t.Fatal("valid cache was not used")
	}
	// An append changes the stamp, so the stale entry is rebuilt.
	commitTestEvents(t, s, "m2", testEvent(3, "", MessageAppendPayload{Messages: []ai.Message{eventText("u2", ai.RoleUser, "second")}}))
	got, err := store.overview(ctx, s.ID())
	if err != nil || got.Messages != 2 || got.LastPrompt != "second" {
		t.Fatalf("%+v %v", got, err)
	}
	// An unfinished last line is not committed history and does not invalidate.
	f, _ := os.OpenFile(s.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"batch_version":1`)
	_ = f.Close()
	if again, err := store.overview(ctx, s.ID()); err != nil || again != got {
		t.Fatalf("unfinished tail changed the overview: %+v %v", again, err)
	}
	// Damaged or foreign cache content is ignored, never trusted.
	for _, bad := range []string{"{", `{"stamp":{},"overview":{"session":"other"}}`, ""} {
		_ = os.WriteFile(cacheFile, []byte(bad), 0o600)
		if again, err := store.overview(ctx, s.ID()); err != nil || again != got {
			t.Fatalf("bad cache %q: %+v %v", bad, again, err)
		}
	}
	if first.Messages != 1 {
		t.Fatal("first overview changed")
	}
}

func TestStampOf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	st := func() stamp {
		t.Helper()
		got, err := stampOf(path)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("")
	if st() != (stamp{}) {
		t.Fatal("empty file has a stamp")
	}
	write("partial")
	if st() != (stamp{}) {
		t.Fatal("no complete line yet")
	}
	write("one\n")
	a := st()
	write("one\npartial")
	if st() != a {
		t.Fatal("unfinished tail leaked into the stamp")
	}
	write("one\ntwo\n")
	b := st()
	if b == a || b.Size != 8 {
		t.Fatalf("append must change it: %+v %+v", a, b)
	}
	// Lines longer than the read chunk are found whole.
	big := strings.Repeat("x", 200<<10)
	write("head\n" + big + "\n")
	c := st()
	write("other\n" + big + "\n")
	if c.LastLine != st().LastLine || c.Size == st().Size {
		t.Fatalf("big last line: %+v vs %+v", c, st())
	}
	write("head\n" + big + "y\n")
	if st().LastLine == c.LastLine {
		t.Fatal("changed last line kept its digest")
	}
}
