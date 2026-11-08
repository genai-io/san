package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestIdentityMetadataRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	open := func() *Store {
		t.Helper()
		s, err := NewStore(dir, "project")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	if err := s.Start(ctx, StartCommand{SessionID: "original", Workspace: "/worktrees/topic", Cwd: "/worktrees/topic/sub", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	check := func(s *Store, id string) {
		t.Helper()
		tx, err := s.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		meta := MetadataFromTranscript(tx)
		if meta.ProjectID != "project" || meta.Workspace != "/worktrees/topic" || meta.Cwd != "/worktrees/topic/sub" {
			t.Fatalf("metadata: %+v", meta)
		}
		for range 2 {
			summary, err := s.summary(ctx, id, true)
			if err != nil {
				t.Fatal(err)
			}
			if summary.ProjectID != meta.ProjectID || summary.Workspace != meta.Workspace {
				t.Fatalf("summary: %+v", summary)
			}
		}
	}
	check(s, "original")
	if err := s.Fork(ctx, ForkCommand{SourceSessionID: "original", NewSessionID: "fork", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	check(s, "fork")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	defer s.Close()
	check(s, "original")
	check(s, "fork")
	data, err := os.ReadFile(s.TranscriptPath("original"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"projectId":"project"`)) || !bytes.Contains(data, []byte(`"workspace":"/worktrees/topic"`)) {
		t.Fatalf("missing optional identity fields: %s", data)
	}
}

func TestIdentityMetadataLegacyCacheRebuild(t *testing.T) {
	ctx := context.Background()
	s, err := NewStore(t.TempDir(), "project")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Write an actual schema4 start lacking the additive fields, rather than
	// using Start, which always stamps the owning project for new records.
	w, done, err := s.writerLocked("old")
	if err != nil {
		t.Fatal(err)
	}
	_, err = writeRecord(w, nil, Record{SessionID: "old", Type: SessionStarted, Version: "4", Cwd: "/old/cwd", Time: time.Now(), Start: &SessionStart{Model: "old-model"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := done(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.TranscriptPath("old"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.log.Stat(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	cache := summaryCache{Size: info.Size, ModifiedAt: info.ModTime, Item: Summary{ID: "old", Model: "stale"}}
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.summaryPath("old"), data, 0600); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Load(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	if tx.ProjectID != "" || tx.Workspace != "" {
		t.Fatalf("invented legacy provenance: %+v", tx)
	}
	summary, err := s.summary(ctx, "old", true)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Model != "old-model" || summary.ProjectID != "" || summary.Workspace != "" {
		t.Fatalf("summary: %+v", summary)
	}
	data, err = os.ReadFile(s.summaryPath("old"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	if cache.Version != summaryCacheVersion {
		t.Fatalf("cache version: %d", cache.Version)
	}
	// A current disk cache is reused, not regenerated on each reopen.
	cache.Item.Model = "cache-reused"
	data, err = json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.summaryPath("old"), data, 0600); err != nil {
		t.Fatal(err)
	}
	clear(s.summaries)
	summary, err = s.summary(ctx, "old", true)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Model != "cache-reused" {
		t.Fatal("current cache was unnecessarily regenerated")
	}
	after, err := os.ReadFile(s.TranscriptPath("old"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("metadata read rewrote the log")
	}
}

func TestIdentityMetadataSidechainHeader(t *testing.T) {
	s, err := NewStore(t.TempDir(), "project")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Start(ctx, StartCommand{SessionID: "child", Workspace: "/worktree", Sidechain: true, Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, full := range []bool{false, false, true, true} {
		summary, err := s.summary(ctx, "child", full)
		if err != nil {
			t.Fatal(err)
		}
		if summary.ProjectID != "project" || summary.Workspace != "/worktree" {
			t.Fatalf("full=%v summary=%+v", full, summary)
		}
	}
}
