package fs

import (
	"context"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
)

func TestBackgroundShellCapturesLaunchingAgent(t *testing.T) {
	manager := task.Default()
	previousSession := manager.SessionID()
	defer manager.SetSessionID(previousSession)
	for _, lifetime := range []task.Lifetime{task.LifetimeJob, task.LifetimeService} {
		for _, owner := range []string{"", "launch-child"} {
			t.Run(string(lifetime)+"/"+owner, func(t *testing.T) {
				manager.SetSessionID("other-conversation")
				ctx := task.WithSessionID(context.Background(), "launch-conversation")
				// The tool caller, not an inherited task owner, determines ownership.
				ctx = task.WithOwnerAgentID(ctx, "stale-owner")
				ctx = tool.WithAgentID(ctx, owner)
				result := (&ShellTool{}).executeBackground(ctx, "sleep 5", "owner test", t.TempDir(), 10*time.Second, lifetime)
				if !result.Success {
					t.Fatal(result.Error)
				}
				meta := result.HookResponse.(map[string]any)["backgroundTask"].(map[string]any)
				id := meta["taskId"].(string)
				bg, ok := manager.Get(id)
				if !ok {
					t.Fatal("missing background task")
				}
				defer manager.Remove(id)
				defer manager.Kill(id)
				manager.SetSessionID("switched-conversation")
				if info := bg.GetStatus(); info.OwnerAgentID != owner || info.ParentSessionID != "launch-conversation" || info.Lifetime != lifetime {
					t.Fatalf("launch ownership = %+v", info)
				}
				if err := manager.Kill(id); err != nil {
					t.Fatal(err)
				}
				if info := bg.GetStatus(); info.OwnerAgentID != owner || info.ParentSessionID != "launch-conversation" {
					t.Fatalf("terminal ownership = %+v", info)
				}
			})
		}
	}
}
