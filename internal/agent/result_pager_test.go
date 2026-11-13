package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// A result the window can hold is passed through untouched: nil means "leave
// it alone", and returning a copy instead would rewrite every tool call for
// nothing.
func TestResultPagerBoundaryAndFailedPersistence(t *testing.T) {
	mainSession := mainSessionFixture(t)
	pager := ResultPager{Store: mainSession.Store, SessionID: mainSession.Session.ID()}
	out, err := pager.Filter(context.Background(), sdkagent.PostToolContext{Result: sdkagent.TextResult(strings.Repeat("x", 100_000))})
	if err != nil || out != nil {
		t.Fatal("threshold result was rewritten")
	}
	out, err = pager.Filter(context.Background(), sdkagent.PostToolContext{
		Call:   ai.ToolCall{ID: "../invalid"},
		Result: sdkagent.Result{Content: ai.TextContent(strings.Repeat("界", 40_000)), Details: "kept"},
	})
	if err != nil || out == nil {
		t.Fatalf("filter: %v, %v", out, err)
	}
	text := out.Content.Text()
	if !utf8.ValidString(text) || !strings.HasPrefix(text, strings.Repeat("界", 3333)+"\n\n") ||
		!strings.Contains(text, "Output truncated from 120000 bytes — full content not persisted") || out.Details != "kept" {
		t.Fatal("preview boundary, failure diagnostic or result metadata changed")
	}
}

func TestResultPagerPersistsUnderNativeOwner(t *testing.T) {
	fixture := mainSessionFixture(t)
	pager := ResultPager{Store: fixture.Store, SessionID: fixture.Session.ID()}
	full := strings.Repeat("界", 40_000)
	original := sdkagent.Result{Content: ai.TextContent(full), Details: "raw details"}
	out, err := pager.Filter(t.Context(), sdkagent.PostToolContext{Call: ai.ToolCall{ID: "native-call"}, Result: original})
	if err != nil || out == nil {
		t.Fatalf("filter=%+v err=%v", out, err)
	}
	if out.Details != "raw details" || original.Content.Text() != full || !utf8.ValidString(out.Content.Text()) {
		t.Fatal("preview changed original result or details")
	}
	marker := "[Full output persisted to blobs/tool-result/" + fixture.Session.ID() + "/native-call]"
	if !strings.HasSuffix(out.Content.Text(), marker) {
		t.Fatalf("marker changed: %q", out.Content.Text()[len(out.Content.Text())-len(marker):])
	}
	path := filepath.Join(filepath.Dir(fixture.Session.Path()), "blobs", "tool-result", "native-call")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != full {
		t.Fatalf("native asset=%d err=%v", len(data), err)
	}
}

func TestAResultThatFitsIsLeftAlone(t *testing.T) {
	pager := ResultPager{}
	out, err := pager.Filter(context.Background(), sdkagent.PostToolContext{
		Result: sdkagent.Result{Content: ai.TextContent(strings.Repeat("x", 1000))},
	})
	if err != nil || out != nil {
		t.Fatalf("trimOverflow = (%v, %v), want (nil, nil)", out, err)
	}
}

// One too large is replaced, and the replacement is what the model is told —
// the guard used to edit the copy the interface draws, which stopped reaching
// the model once the agent held its own conversation.
func TestAResultTooLargeIsReplacedForTheModel(t *testing.T) {
	pager := ResultPager{}
	full := strings.Repeat("y", 200_000)
	out, err := pager.Filter(context.Background(), sdkagent.PostToolContext{
		Call:   ai.ToolCall{ID: "tc-1"},
		Result: sdkagent.Result{Content: ai.TextContent(full)},
	})
	if err != nil {
		t.Fatalf("trimOverflow: %v", err)
	}
	if out == nil {
		t.Fatal("a 200KB result was sent to the model whole")
	}
	got := out.Content.Text()
	if len(got) >= len(full) {
		t.Errorf("replacement is %d bytes, no smaller than the %d it replaced", len(got), len(full))
	}
	if !strings.Contains(got, "truncated") && !strings.Contains(got, "persisted") {
		t.Errorf("the model was not told the rest exists: %q", got[max(0, len(got)-120):])
	}
}
