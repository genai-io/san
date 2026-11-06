package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestUpdateNoticeAboveComposer(t *testing.T) {
	for _, width := range []int{100, 40, 24} {
		m := modelWithInferenceProgress(24)
		m.env.Width = width
		m.updateInstalled = "1.25.1"
		separator := strings.Repeat("─", width-1)
		footer, inputRow := m.renderFooter(separator)
		lines := strings.Split(ansi.Strip(footer), "\n")
		notice := lines[inputRow-2]
		if !strings.Contains(notice, "Restart to update") || lines[inputRow-1] != separator {
			t.Fatalf("notice not above composer: %s", footer)
		}
		if ansi.StringWidth(notice) != width-1 {
			t.Fatalf("notice not right-aligned: %q", notice)
		}
		if strings.Contains(strings.Join(lines[inputRow:], "\n"), "Restart to update") {
			t.Fatal("update notice remains below composer")
		}
		frame, cursor := m.renderNormalView(separator, "")
		if strings.Count(frame, "\n")+1 != 24 || cursor == nil || cursor.Position.Y != 21 {
			t.Fatalf("composer displaced: cursor=%+v\n%s", cursor, frame)
		}
	}
}

func TestUpdateNoticeVisibility(t *testing.T) {
	var m model
	m.env.Width = 80
	if m.renderUpdateNotice() != "" {
		t.Fatal("notice shown without an installed update")
	}
	m.updateInstalled = "1.25.1"
	if !strings.Contains(ansi.Strip(m.renderUpdateNotice()), "v1.25.1 installed · Restart to update") {
		t.Fatal("missing version")
	}
	m.env.Width = 10
	if m.renderUpdateNotice() != "" {
		t.Fatal("notice overflows narrow terminal")
	}
}
