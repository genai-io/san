package group

import (
	"strings"
	"testing"

	members "github.com/genai-io/san/internal/group"
)

func TestJoinModeLeave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	members.BindSession(func() string { return "s-self" })
	cwd := "/work/shop/web"

	out, err := run(map[string]any{"action": "join", "group": "shop", "role": "the checkout page"}, cwd)
	if err != nil || !strings.HasPrefix(out, "Joined group shop as @web (active).") || !strings.Contains(out, `<group name="shop">`) {
		t.Fatalf("join = %q, %v; want the directory's name and the roster", out, err)
	}
	if out, err := run(map[string]any{"action": "mode", "mode": "passive"}, cwd); err != nil || out != "You are now passive in group shop." {
		t.Errorf("mode = %q, %v", out, err)
	}
	if _, err := run(map[string]any{"action": "mode", "mode": "sleepy"}, cwd); err == nil {
		t.Error("an unknown mode was accepted")
	}
	if out, err := run(map[string]any{"action": "leave"}, cwd); err != nil || !strings.HasPrefix(out, "Left group shop") {
		t.Errorf("leave = %q, %v", out, err)
	}
	if _, err := run(map[string]any{"action": "status"}, cwd); err == nil {
		t.Error("status was accepted; the roster reminder already says it")
	}
	if _, err := run(map[string]any{"action": "kick"}, cwd); err == nil {
		t.Error("kick is not the tool's to do, yet it was accepted")
	}
}
