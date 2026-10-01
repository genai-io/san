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
		return xansi.Strip(RenderModeStatus(OperationModeParams{Mode: mode, ModelName: "M", Width: 100, Group: "shop", GroupSelf: "api", GroupWaiting: waiting}))
	}
	if got := line(setting.ModeBypassPermissions, 0); !strings.HasPrefix(got, "  ⏵⏵ YOLO (shift+tab to cycle)  ◆ shop (api)") {
		t.Errorf("after the mode: %q", got)
	}
	if got := line(setting.ModeNormal, 2); !strings.HasPrefix(got, "  ◆ shop (api) · 2 waiting") {
		t.Errorf("no mode: %q", got)
	}
}
