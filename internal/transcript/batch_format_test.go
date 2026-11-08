package transcript

import (
	"context"
	"os"
	"testing"
)

func TestBatchMixedLogsRefuseLegacyReplayAndFork(t *testing.T) {
	for _, batchFirst := range []bool{false, true} {
		name := "legacy-first"
		if batchFirst {
			name = "batch-first"
		}
		t.Run(name, func(t *testing.T) {
			s := batchStore(t)
			ctx := context.Background()
			release, err := s.Claim("batch")
			if err != nil {
				t.Fatal(err)
			}
			tail, err := s.BatchTail(ctx, "batch")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CommitBatch(ctx, "batch", eventBatch("b", tail)); err != nil {
				t.Fatal(err)
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
			if err := s.Start(ctx, StartCommand{SessionID: "legacy"}); err != nil {
				t.Fatal(err)
			}
			batch, err := os.ReadFile(s.TranscriptPath("batch"))
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := os.ReadFile(s.TranscriptPath("legacy"))
			if err != nil {
				t.Fatal(err)
			}
			mixed := append(legacy, batch...)
			if batchFirst {
				mixed = append(batch, legacy...)
			}
			if err := os.WriteFile(s.TranscriptPath("mixed"), mixed, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(ctx, "mixed"); err == nil {
				t.Fatal("accepted mixed log")
			}
			if err := s.Fork(ctx, ForkCommand{SourceSessionID: "mixed", NewSessionID: "fork"}); err == nil {
				t.Fatal("forked mixed log")
			}
			if exists, _ := s.Exists(ctx, "fork"); exists {
				t.Fatal("created destination")
			}
			after, err := os.ReadFile(s.TranscriptPath("mixed"))
			if err != nil || string(after) != string(mixed) {
				t.Fatal("modified mixed log", err)
			}
		})
	}
}
