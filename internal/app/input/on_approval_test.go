package input

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/v2/internal/tool/perm"
)

// TestApprovalModalDigitKeysFollowOptionOrder confirms the digit key dispatch
// is indexed off buildApprovalOptionRows rather than hard-coded cases. Adding
// a new row at any position should automatically map to a new digit without
// touching HandleKeypress.
func TestApprovalModalDigitKeysFollowOptionOrder(t *testing.T) {
	model := &ApprovalModel{
		active:  true,
		request: &perm.PermissionRequest{ToolName: "Bash"},
	}
	options := buildApprovalOptionRows(model.request)

	cases := []struct {
		key      rune
		wantOpt  int
		wantResp ApprovalResponseMsg
	}{
		{'1', 0, ApprovalResponseMsg{Approved: true}},
		{'2', 1, ApprovalResponseMsg{Approved: true, AllowAll: true}},
		{'3', 2, ApprovalResponseMsg{Approved: true, Persist: true}},
		{'4', 3, ApprovalResponseMsg{}},
	}
	for _, tc := range cases {
		model.active = true
		_, resp := model.HandleKeypress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
		if resp == nil {
			t.Fatalf("digit %q produced no response", string(tc.key))
		}
		want := options[tc.wantOpt]
		if resp.Approved != want.Approved || resp.AllowAll != want.AllowAll || resp.Persist != want.Persist {
			t.Errorf("digit %q: response = %+v, want option %d %+v",
				string(tc.key), *resp, tc.wantOpt, want)
		}
	}
}

// Up/Down boundaries must follow len(options)-1, not a hardcoded 3.
func TestApprovalModalArrowBoundsFollowOptionCount(t *testing.T) {
	model := &ApprovalModel{
		active:      true,
		request:     &perm.PermissionRequest{ToolName: "Bash"},
		selectedIdx: 0,
	}
	options := buildApprovalOptionRows(model.request)

	// Walk down past the end — should clamp at len-1, not crash, not exceed.
	for i := 0; i < len(options)+3; i++ {
		model.HandleKeypress(tea.KeyMsg{Type: tea.KeyDown})
	}
	if model.selectedIdx != len(options)-1 {
		t.Errorf("after %d downs: selectedIdx=%d, want %d (clamp at last)",
			len(options)+3, model.selectedIdx, len(options)-1)
	}

	// Walk up past the start — should clamp at 0.
	for i := 0; i < len(options)+3; i++ {
		model.HandleKeypress(tea.KeyMsg{Type: tea.KeyUp})
	}
	if model.selectedIdx != 0 {
		t.Errorf("after walking up: selectedIdx=%d, want 0", model.selectedIdx)
	}
}

// Shift+Tab finds the AllowAll row by its flag, not by index. If the modal
// reorders options, the accelerator still hits the right row.
func TestApprovalModalShiftTabFindsAllowAllByFlag(t *testing.T) {
	model := &ApprovalModel{
		active:  true,
		request: &perm.PermissionRequest{ToolName: "Skill"},
	}
	_, resp := model.HandleKeypress(tea.KeyMsg{Type: tea.KeyShiftTab})
	if resp == nil {
		t.Fatal("ShiftTab produced no response")
	}
	if !resp.Approved || !resp.AllowAll {
		t.Errorf("ShiftTab response = %+v, want {Approved:true, AllowAll:true}", *resp)
	}
}

// Esc/Ctrl+C maps to the first row with Approved=false, again by flag rather
// than index — so reordering "No" or replacing it with a richer reject row
// still works.
func TestApprovalModalEscFindsRejectByFlag(t *testing.T) {
	model := &ApprovalModel{
		active:  true,
		request: &perm.PermissionRequest{ToolName: "Bash"},
	}
	_, resp := model.HandleKeypress(tea.KeyMsg{Type: tea.KeyEsc})
	if resp == nil {
		t.Fatal("Esc produced no response")
	}
	if resp.Approved {
		t.Errorf("Esc response Approved = true, want false")
	}
}

// BuildApprovalOptions and buildApprovalOptionRows must stay aligned in
// length and label order — they share a producer so a regression here means
// the public helper drifted from the internal source.
func TestBuildApprovalOptionsMatchesRows(t *testing.T) {
	req := &perm.PermissionRequest{ToolName: "Edit"}
	labels := BuildApprovalOptions(req)
	rows := buildApprovalOptionRows(req)
	if len(labels) != len(rows) {
		t.Fatalf("len(labels)=%d, len(rows)=%d", len(labels), len(rows))
	}
	for i := range rows {
		if labels[i] != rows[i].Label {
			t.Errorf("label[%d] = %q, row label = %q", i, labels[i], rows[i].Label)
		}
	}
}
