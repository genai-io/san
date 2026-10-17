package transcript

import (
	"context"
	"os"
	"testing"
)

func TestForkCannotOverwriteExistingOrClaimedDestination(t *testing.T) {
	for _, kind := range []string{"source", "existing", "claimed"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			s, err := NewStore(dir, "p")
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := s.Start(ctx, StartCommand{SessionID: "source"}); err != nil {
				t.Fatal(err)
			}
			dest := kind
			if kind == "existing" {
				if err := s.Start(ctx, StartCommand{SessionID: dest}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "claimed" {
				other, err := NewStore(dir, "p")
				if err != nil {
					t.Fatal(err)
				}
				release, err := other.Claim(dest)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			before, _ := os.ReadFile(s.TranscriptPath(dest))
			if err := s.Fork(ctx, ForkCommand{SourceSessionID: "source", NewSessionID: dest}); err == nil {
				t.Fatal("fork accepted an occupied destination")
			}
			after, _ := os.ReadFile(s.TranscriptPath(dest))
			if string(after) != string(before) {
				t.Fatal("fork changed the occupied destination")
			}
		})
	}
}
