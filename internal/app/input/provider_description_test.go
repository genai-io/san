package input

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/llm"
)

func TestProviderDescriptionMapping(t *testing.T) {
	got := newProviderModelItem(llm.ModelInfo{ID: "a", Description: "Remote description"}, "test", "", nil)
	if got.Description != "Remote description" {
		t.Fatal(got)
	}
}

func TestProviderDescriptionSelectionAndPaging(t *testing.T) {
	s := NewProviderSelector()
	s.active, s.width, s.height = true, 100, 30
	for i := 0; i < 30; i++ {
		m := &providerModelItem{ID: "model", Name: "model", Description: "First description"}
		if i > 0 {
			m.Description = "Next description"
		}
		s.visibleItems = append(s.visibleItems, providerListItem{Kind: providerItemModel, Model: m})
	}
	baseline := lipgloss.Height(s.Render())
	if !strings.Contains(ansi.Strip(s.Render()), "First description") {
		t.Fatal("missing first description")
	}
	s.MoveDown()
	if got := ansi.Strip(s.Render()); !strings.Contains(got, "Next description") || strings.Contains(got, "First description") {
		t.Fatal(got)
	}
	for i := 0; i < 28; i++ {
		s.MoveDown()
	}
	if s.selectedIdx < s.scrollOffset || s.selectedIdx >= s.scrollOffset+s.modelListLimit() {
		t.Fatal("selection outside viewport")
	}
	if lipgloss.Height(s.Render()) != baseline {
		t.Fatal("paging changed viewport height")
	}
	s.visibleItems[s.selectedIdx].Model.Description = ""
	if s.renderModelDescription() != "" || lipgloss.Height(s.Render()) != baseline {
		t.Fatal("empty description changed viewport")
	}
	s.visibleItems[s.selectedIdx].Model.Description = "hidden"
	s.activeTab = providerTabProviders
	if s.renderModelDescription() != "" {
		t.Fatal("description on Providers tab")
	}
}

func TestProviderDescriptionPlainTextAndWidth(t *testing.T) {
	for _, width := range []int{12, 40, 100} {
		s := NewProviderSelector()
		s.width, s.height = width, 18
		m := &providerModelItem{Description: "\x1b[31m安全\x1b[0m\x1b]0;title\a\r\n\t" + strings.Repeat("長い説明text", 100) + "\x00\u202e"}
		s.visibleItems = []providerListItem{{Kind: providerItemModel, Model: m}}
		got := ansi.Strip(s.renderModelDescription())
		if strings.ContainsAny(got, "\x1b\x00\r\t\a\u202e") || strings.Contains(got, "title") {
			t.Fatalf("unsafe description: %q", got)
		}
		lines := strings.Split(got, "\n")
		if len(lines) > 2 {
			t.Fatal("too many detail rows")
		}
		for _, line := range lines {
			if lipgloss.Width(line) > max(3, width-8) {
				t.Fatalf("overflow at %d: %q", width, line)
			}
		}
	}
}
