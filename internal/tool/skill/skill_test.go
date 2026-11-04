package skill

import (
	"context"
	"strings"
	"testing"

	registry "github.com/genai-io/san/internal/skill"
)

func TestUserOnlySkillRejected(t *testing.T) {
	previous := registry.Default()
	t.Cleanup(func() { registry.SetDefaultRegistry(previous) })
	t.Setenv("HOME", t.TempDir())
	registry.Initialize(registry.Options{CWD: t.TempDir()})
	group, _ := registry.Default().Get("group")
	tool := &SkillTool{}
	for _, state := range []registry.SkillState{registry.StateEnable, registry.StateActive} {
		group.State = state
		for _, name := range []string{"group", "GROUP"} {
			params := map[string]any{"skill": name}
			request, err := tool.PreparePermission(context.Background(), params, "")
			if request != nil || err == nil || !strings.Contains(err.Error(), "user-only") {
				t.Fatalf("permission for %s/%s: request=%v err=%v", state, name, request, err)
			}
			for _, result := range []bool{
				tool.Execute(context.Background(), params, "").Success,
				tool.ExecuteApproved(context.Background(), params, "").Success,
			} {
				if result {
					t.Fatalf("user-only skill executed in state %s via %s", state, name)
				}
			}
		}
	}
}

func TestOrdinaryEnabledSkillStillInvocable(t *testing.T) {
	previous := registry.Default()
	t.Cleanup(func() { registry.SetDefaultRegistry(previous) })
	registry.SetDefaultRegistry(registry.NewRegistryForTest(map[string]*registry.Skill{
		"ordinary": {Name: "ordinary", State: registry.StateEnable, InlineInstructions: "Do the work."},
	}, nil, nil))
	tool := &SkillTool{}
	params := map[string]any{"skill": "ordinary"}
	if _, err := tool.PreparePermission(context.Background(), params, ""); err != nil {
		t.Fatal(err)
	}
	if result := tool.Execute(context.Background(), params, ""); !result.Success || !strings.Contains(result.Output, "Do the work.") {
		t.Fatalf("ordinary enabled skill failed: %+v", result)
	}
}
