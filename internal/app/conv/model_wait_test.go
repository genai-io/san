package conv

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestModelWaitQuietBreathAndSilence(t *testing.T) {
	started := time.Now().Add(-17 * time.Second)
	first := renderModelWait(0, 80, started, started, "")
	peak := renderModelWait(3, 80, started, started, "")
	if ansi.Strip(first) != "✦ On it · 17s" || ansi.Strip(peak) != "✦ On it · 17s" || first == peak {
		t.Fatalf("normal wait must breathe with fixed concise copy and stable geometry: %q / %q", first, peak)
	}
	old := time.Now().Add(-time.Minute)
	if row := ansi.Strip(renderModelWait(0, 80, old, old, "")); row != "✦ Waiting for model · 1m 0s" {
		t.Fatalf("prolonged silence needs a truthful explanation: %q", row)
	}
	if row := ansi.Strip(renderModelWait(0, 80, old, time.Now(), "")); row != "✦ On it · 1m 0s" {
		t.Fatalf("recent data must clear the explanation, not reset inference time: %q", row)
	}
	if row := ansi.Strip(renderModelWait(0, 80, old, old, "Retrying · attempt 2")); row != "✦ Retrying · attempt 2 · 1m 0s" {
		t.Fatalf("real retry must keep its attempt label: %q", row)
	}
}

func TestInferenceSilenceClockOnlyTracksRealData(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event ai.Event
		fresh bool
	}{
		{"text", ai.Event{Type: ai.EventBlockDelta, Block: ai.TextBlock("new text")}, true},
		{"thinking", ai.Event{Type: ai.EventBlockDelta, Block: ai.Block{Type: ai.BlockThinking, Text: "new reasoning"}}, true},
		{"tool input", ai.Event{Type: ai.EventProgress, Progress: &ai.Progress{ToolName: "Write", InputBytes: 200}}, true},
		{"duplicate progress", ai.Event{Type: ai.EventProgress, Progress: &ai.Progress{ToolName: "Write", InputBytes: 100}}, false},
		{"empty text", ai.Event{Type: ai.EventBlockDelta, Block: ai.TextBlock("")}, false},
		{"block boundary", ai.Event{Type: ai.EventBlockStart, Block: ai.TextBlock("")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, rt := NewModel(80), &streamRuntime{}
			applyAgentEvent(rt, &m, sdkagent.InferenceStart{Attempt: 1})
			applyChunk(rt, &m, ai.Event{Type: ai.EventProgress, Progress: &ai.Progress{ToolName: "Write", InputBytes: 100}})
			old := time.Now().Add(-time.Minute)
			m.Stream.StartedAt, m.Stream.LastDataAt = old, old
			m.AddNotice("background activity is not model data")
			m.HandleActivityTick(false)
			if !m.Stream.LastDataAt.Equal(old) || !strings.Contains(inferenceView(&m, 80), "Waiting for model") {
				t.Fatal("background notices or UI ticks reset the silence clock")
			}
			applyChunk(rt, &m, tc.event)
			if got := m.Stream.LastDataAt.After(old); got != tc.fresh {
				t.Fatalf("fresh data = %v, want %v", got, tc.fresh)
			}
			if waiting := strings.Contains(inferenceView(&m, 80), "Waiting for model"); waiting == tc.fresh {
				t.Fatalf("silence explanation did not follow real data: %q", inferenceView(&m, 80))
			}
		})
	}
}

func TestModelWaitDefersEntirelyToModal(t *testing.T) {
	p := AssistantParams{StreamActive: true, IsLast: true, DockedModalActive: true, WaitStartedAt: time.Now().Add(-time.Minute), Width: 80}
	if row := p.waitingRow(); row != "" {
		t.Fatalf("modal has its own explanation, not a second waiting marker: %q", row)
	}
}
