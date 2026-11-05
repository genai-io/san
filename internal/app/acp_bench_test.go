package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

// Both modes use the same native runtime, transcript and in-process ACP pipes.
// Restart reproduces the old prompt boundary; history setup is not timed.
func BenchmarkACPPrompt(b *testing.B) {
	for _, size := range []int{0, 100, 1000} {
		for _, restart := range []bool{true, false} {
			b.Run(fmt.Sprintf("history=%d/restart=%v", size, restart), func(b *testing.B) {
				f := newACPFixture(b, aitest.Always(aitest.Says("done")), nil)
				id := f.session(b, b.TempDir())
				s := f.host.sessions[id]
				history := make([]core.Message, size)
				for i := range history {
					history[i] = core.UserMessage("fixed history", nil)
				}
				if _, err := f.prompt(id, "warmup"); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					s.runtime.SetMessages(history)
					b.StartTimer()
					if restart {
						if err := s.runtime.Stop(context.Background()); err != nil {
							b.Fatal(err)
						}
					}
					if _, err := f.prompt(id, "hello"); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
