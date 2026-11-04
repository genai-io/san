package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/filecache"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/fs"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestACPBashDirHelp(t *testing.T) {
	for _, schema := range (agent.BuildParams{BashDirHelp: acpBashDirHelp}).Schemas() {
		if schema.Name == "Bash" {
			if strings.Contains(schema.Description, "updates the session working directory") || !strings.Contains(schema.Description, "subsequent commands start in the original session working directory") {
				t.Fatal(schema.Description)
			}
			return
		}
	}
	t.Fatal("missing Bash schema")
}

func TestACPNativeHandoffOwners(t *testing.T) {
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			dir := t.TempDir()
			path := filepath.Join(dir, "created")
			s := &acpSession{hooks: hook.NewEngine(&setting.Data{}, "owner", dir, ""), params: agent.BuildParams{FileCache: filecache.New()}}
			changed := make(chan string, 2)
			s.hooks.AddSessionFunctionHook(hook.FileChanged, "", hook.FunctionHook{Callback: func(_ context.Context, in hook.HookInput) (hook.HookOutput, error) {
				changed <- in.FilePath
				return hook.HookOutput{}, nil
			}})
			views := fs.NewFileViews()
			opts := []tool.AdaptOption{tool.WithSideEffectHandler(func(id string, v any) { s.sideEffects.Store(id, v) }), tool.WithToolContext(func(ctx context.Context) context.Context { return fs.WithFileViews(ctx, views) })}
			adapted := tool.AdaptTool(&fs.WriteTool{}, core.ToolSchema{Name: "Write"}, func() string { return dir }, opts...)
			result, err := adapted.Run(core.WithToolCallID(context.Background(), "collision"), ai.ToolCall{ID: "collision", Input: `{"file_path":"created","content":"owned content"}`})
			if err != nil {
				t.Error(err)
				return
			}
			s.postTool(sdkagent.ToolEnd{ID: "collision", Name: "Write", Result: result})
			s.postTool(sdkagent.ToolEnd{ID: "collision", Name: "Write", Result: result})
			editor := tool.AdaptTool(&fs.EditTool{}, core.ToolSchema{Name: "Edit"}, func() string { return dir }, opts...)
			result, err = editor.Run(core.WithToolCallID(context.Background(), "collision"), ai.ToolCall{ID: "collision", Input: `{"file_path":"created","old_string":"owned content","new_string":"edited content"}`})
			if err != nil {
				t.Error(err)
			}
			s.postTool(sdkagent.ToolEnd{ID: "collision", Name: "Edit", Result: result})
			s.hooks.Shutdown()
			if len(changed) != 2 {
				t.Errorf("FileChanged count=%d", len(changed))
			} else if got := <-changed; got != path {
				t.Errorf("wrong owner path: %s", got)
			}
			if _, ok := s.sideEffects.Load("collision"); ok {
				t.Error("handoff not consumed")
			}
			files, _ := s.params.FileCache.RestoreRecent()
			if len(files) != 1 || files[0].FilePath != path {
				t.Errorf("restore: %+v", files)
			}
		})
	}
	wg.Wait()
	if tool.PopSideEffect("collision") != nil {
		t.Fatal("handoff leaked to global store")
	}
}
