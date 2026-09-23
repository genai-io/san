// The reasoning-display preference at the render layer: only "full" draws the
// body, "collapsed" draws a single duration line in its place, "hidden" draws
// nothing. These are the unit-level guarantees behind the scrollback tests in
// internal/app, which cover the same gate on the commit path.
package conv

import (
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/setting"
)

const bodySentinel = "REASONING_BODY_SENTINEL"

func TestRenderAssistantMessageThinkingDisplay(t *testing.T) {
	settled := func() AssistantParams {
		return AssistantParams{
			Thinking:         bodySentinel,
			Content:          "the answer",
			Width:            80,
			ThinkingDuration: 3200 * time.Millisecond,
		}
	}

	t.Run("full draws the body and no duration line", func(t *testing.T) {
		p := settled()
		p.ThinkingDisplay = setting.ThinkingDisplayFull
		out := stripANSI(RenderAssistantMessage(p))
		if !strings.Contains(out, bodySentinel) {
			t.Fatalf("full mode should draw the reasoning body:\n%s", out)
		}
		if strings.Contains(out, "Thought") {
			t.Fatalf("full mode should not draw a duration line:\n%s", out)
		}
	})

	t.Run("unset mode keeps the historical behaviour", func(t *testing.T) {
		out := stripANSI(RenderAssistantMessage(settled()))
		if !strings.Contains(out, bodySentinel) {
			t.Fatalf("an unset mode must not lose the reasoning:\n%s", out)
		}
	})

	t.Run("collapsed replaces the body with the duration", func(t *testing.T) {
		p := settled()
		p.ThinkingDisplay = setting.ThinkingDisplayCollapsed
		out := stripANSI(RenderAssistantMessage(p))
		if strings.Contains(out, bodySentinel) {
			t.Fatalf("collapsed mode leaked the reasoning body:\n%s", out)
		}
		if !strings.Contains(out, "Thought for 3.2s") {
			t.Fatalf("collapsed mode should draw the duration line:\n%s", out)
		}
		if !strings.Contains(out, "the answer") {
			t.Fatalf("collapsed mode must still draw the content:\n%s", out)
		}
	})

	t.Run("hidden draws neither", func(t *testing.T) {
		p := settled()
		p.ThinkingDisplay = setting.ThinkingDisplayHidden
		out := stripANSI(RenderAssistantMessage(p))
		if strings.Contains(out, bodySentinel) {
			t.Fatalf("hidden mode leaked the reasoning body:\n%s", out)
		}
		if strings.Contains(out, "Thought") {
			t.Fatalf("hidden mode drew a duration line:\n%s", out)
		}
		if !strings.Contains(out, "the answer") {
			t.Fatalf("hidden mode must still draw the content:\n%s", out)
		}
	})
}

// While the message is still the live streaming tail, the duration line is withheld:
// the model is not done reasoning, and a duration line written now would have to be
// rewritten later — which native scrollback cannot do.
func TestRenderAssistantMessageCollapsedWaitsForTheStreamToEnd(t *testing.T) {
	p := AssistantParams{
		Thinking:         bodySentinel,
		StreamActive:     true,
		IsLast:           true,
		Width:            80,
		ThinkingDisplay:  setting.ThinkingDisplayCollapsed,
		ThinkingDuration: time.Second,
	}
	if out := stripANSI(RenderAssistantMessage(p)); strings.Contains(out, "Thought") {
		t.Fatalf("no duration line while reasoning is still streaming:\n%s", out)
	}
}

// ThinkingEmitted means the duration line already went to scrollback mid-stream, so
// the turn-end render must not print it a second time.
func TestRenderAssistantMessageCollapsedDurationLinePrintsOnce(t *testing.T) {
	p := AssistantParams{
		Thinking:         bodySentinel,
		Content:          "the answer",
		Width:            80,
		ThinkingDisplay:  setting.ThinkingDisplayCollapsed,
		ThinkingDuration: time.Second,
		ThinkingEmitted:  true,
	}
	if out := stripANSI(RenderAssistantMessage(p)); strings.Contains(out, "Thought") {
		t.Fatalf("the duration line must print once per message:\n%s", out)
	}
}

// A message restored from a transcript was never timed, so the duration is left
// off rather than reported as zero.
func TestRenderThinkingDurationLineOmitsAnUnmeasuredDuration(t *testing.T) {
	measured := RenderThinkingDurationLine(2500 * time.Millisecond)
	if !strings.Contains(stripANSI(measured), "Thought for 2.5s") {
		t.Fatalf("measured duration line = %q", stripANSI(measured))
	}

	unmeasured := stripANSI(RenderThinkingDurationLine(0))
	if strings.Contains(unmeasured, "0s") {
		t.Fatalf("an unmeasured duration must not read as zero: %q", unmeasured)
	}
	if !strings.Contains(unmeasured, "Thought") {
		t.Fatalf("an unmeasured duration should still say it reasoned: %q", unmeasured)
	}
}
