package conv

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/setting"
)

// The group sits on the left, after the mode, aligned with it when there is
// none; what waits on the person rides along.
func TestGroupStatusSitsLeftAfterTheMode(t *testing.T) {
	line := func(mode setting.OperationMode, waiting int) string {
		return xansi.Strip(RenderModeStatus(OperationModeParams{Mode: mode, ModeHint: true, ModelName: "M", Width: 100, Group: "shop", GroupSelf: "api", GroupWaiting: waiting}))
	}
	if got := line(setting.ModeBypassPermissions, 0); !strings.HasPrefix(got, "  ⏵⏵ YOLO (shift+tab to cycle)  ◆ shop/api") {
		t.Errorf("after the mode: %q", got)
	}
	if got := line(setting.ModeNormal, 2); !strings.HasPrefix(got, "  ◆ shop/api · 2 waiting") {
		t.Errorf("no mode: %q", got)
	}
}

// The shift+tab hint shows only while ModeHint is set: a moment after start
// or a mode switch.
func TestShiftTabHintShowsOnlyForAMoment(t *testing.T) {
	line := func(hint bool) string {
		return xansi.Strip(RenderModeStatus(OperationModeParams{Mode: setting.ModeBypassPermissions, ModeHint: hint, ModelName: "M", Width: 100}))
	}
	if !strings.Contains(line(true), "(shift+tab to cycle)") || strings.Contains(line(false), "shift+tab") {
		t.Errorf("hint on: %q, off: %q", line(true), line(false))
	}
}

// Holding is a quiet fact; only what waits on the person turns the group amber.
func TestHoldShowsQuietlyInTheStatusBar(t *testing.T) {
	render := func(waiting int) string {
		return RenderModeStatus(OperationModeParams{ModelName: "M", Width: 100, Group: "shop", GroupSelf: "web", GroupHold: true, GroupWaiting: waiting})
	}
	if got := xansi.Strip(render(0)); !strings.HasPrefix(got, "  ◆ shop/web · hold") || strings.Contains(got, "waiting") {
		t.Errorf("held, nothing waiting: %q", got)
	}
	if got := xansi.Strip(render(2)); !strings.HasPrefix(got, "  ◆ shop/web · hold · 2 waiting") {
		t.Errorf("held, two waiting: %q", got)
	}
	if render(0) == render(2) {
		t.Error("waiting messages should change how the group reads")
	}
}
