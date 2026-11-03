package app

import (
	"errors"
	"fmt"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
)

func TestManualCompactionInvalidatesPendingStop(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		for _, outcome := range []string{"pending", "failed", "completed"} {
			t.Run(fmt.Sprintf("%s/blocked=%v", outcome, blocked), func(t *testing.T) {
				m, provider := compactQueueModel(t)
				m.services.LLM.SetProvider(provider)
				finish := pendingStopOutcome(t, m, blocked)
				_, cmd, _ := m.executeCommand(t.Context(), "/compact")
				if cmd == nil || !m.conv.Compact.Active || m.pendingStopHook != nil {
					t.Fatal("accepted compaction did not invalidate idle decision")
				}
				switch outcome {
				case "failed":
					m.HandleCompactResult(conv.CompactResultMsg{Context: m.pendingCompaction.ctx, CompactResult: agent.CompactResult{Err: errors.New("summary failed")}})
				case "completed":
					m.OnCompacted(core.Compacted{Summary: "summary", Trigger: "manual"})
				}
				before := len(m.conv.Messages)
				if got := m.handleStopHookResult(finish()); got != nil || len(m.conv.Messages) != before {
					t.Fatal("old Stop result applied after compaction admission")
				}
				select {
				case <-provider.requests:
					t.Fatal("old Stop result started inference")
				default:
				}
			})
		}
	}
}
