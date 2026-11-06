package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestUpdateNoticeSharesInferenceRow(t *testing.T) {
	for _, width := range []int{100, 40, 24} {
		m := modelWithInferenceProgress(24)
		m.env.Width = width
		separator := strings.Repeat("─", width-1)
		before, beforeInput := m.renderFooter(separator)
		m.updateInstalled = "1.25.1"
		footer, inputRow := m.renderFooter(separator)
		lines := strings.Split(ansi.Strip(footer), "\n")
		status := lines[inputRow-2]
		if !strings.Contains(status, "On it") || lines[inputRow-1] != separator {
			t.Fatalf("inference lost input adjacency: %s", footer)
		}
		if inputRow != beforeInput || strings.Count(before, "\n") != strings.Count(footer, "\n") {
			t.Fatal("update notice added a row")
		}
		if width == 100 && (!strings.Contains(status, "v1.25.1 installed · Restart to update") || ansi.StringWidth(status) != width-1) {
			t.Fatalf("notice not right-aligned beside inference: %q", status)
		}
		if ansi.StringWidth(status) >= width {
			t.Fatalf("status row overflows: %q", status)
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

func TestUpdateNoticePriority(t *testing.T) {
	var m model
	m.env.Width = 80
	if got := m.withUpdateNotice("On it"); got != "On it" {
		t.Fatalf("status changed without an update: %q", got)
	}
	m.updateInstalled = "1.25.1"
	if got := ansi.Strip(m.withUpdateNotice("")); !strings.HasSuffix(got, "v1.25.1 installed · Restart to update") || ansi.StringWidth(got) != 79 {
		t.Fatalf("idle notice not right-aligned: %q", got)
	}
	m.env.Width = 26
	if got := ansi.Strip(m.withUpdateNotice("On it")); !strings.Contains(got, "Restart to update") || strings.Contains(got, "installed") {
		t.Fatalf("notice did not compact: %q", got)
	}
	m.env.Width = 24
	if got := m.withUpdateNotice("On it"); got != "On it" {
		t.Fatalf("update displaced task status: %q", got)
	}
	m.env.Width = 10
	if m.withUpdateNotice("") != "" {
		t.Fatal("notice overflows narrow terminal")
	}
}
