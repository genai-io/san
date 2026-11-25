package subagent

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/tool"
	"gopkg.in/yaml.v3"
)

func TestRunningWorkerHonorsParentRevocation(t *testing.T) {
	cwd := t.TempDir()
	var revoked atomic.Bool
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if len(req.Tools) != 1 || req.Tools[0].Name != "Write" {
			t.Errorf("unexpected catalogue %+v", req.Tools)
		}
		revoked.Store(true)
		return aitest.Asks(ai.ToolCall{ID: "revoked", Name: "Write", Input: `{"file_path":"unexpected.txt","content":"no"}`})(ctx, req)
	}, aitest.Says("finished"))
	e := NewExecutor(continuationProvider{driver}, cwd, "aitest", nil)
	store, parent := continuationSessions(t, "owner")
	bindContinuationSessions(t, e, store, parent)
	e.SetParentTools(func() []string {
		if revoked.Load() {
			return []string{}
		}
		return []string{"Write", "Read"}
	})
	_, err := e.Run(context.Background(), tool.AgentExecRequest{Prompt: "test", Tools: []string{"Write"}, ResolvedAgentConfig: &AgentConfig{AllowTools: ToolList{{Name: "Write"}}, PermissionMode: PermissionBypass}})
	if err != nil {
		t.Fatal(err)
	}
	if !revoked.Load() {
		t.Fatal("inference not reached")
	}
	if _, err = os.Stat(filepath.Join(cwd, "unexpected.txt")); !os.IsNotExist(err) {
		t.Fatalf("revoked tool executed: %v", err)
	}
}

func TestWorkerToolSelection(t *testing.T) {
	e := &Executor{}
	e.SetParentTools(func() []string { return []string{"Read", "Agent", "Bash"} })
	for _, tc := range []struct {
		name    string
		tools   []string
		want    []string
		invalid bool
	}{
		{"inherit", nil, []string{"Read", "Bash"}, false}, {"none", []string{}, []string{}, false}, {"subset", []string{"Read"}, []string{"Read"}, false},
		{"unknown", []string{"not-a-tool"}, nil, true}, {"duplicate", []string{"Read", "Read"}, nil, true}, {"parent-only", []string{"Agent"}, nil, true}, {"outside parent", []string{"Skill"}, nil, true}, {"pattern", []string{"Bash(git *)"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pin, err := e.PinWorkerPolicy(tool.AgentExecRequest{Tools: tc.tools})
			if tc.invalid {
				if err == nil {
					t.Fatal("accepted invalid selection")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(pin.Tools)
			slices.Sort(tc.want)
			if pin.Tools == nil || !slices.Equal(pin.Tools, tc.want) {
				t.Fatalf("got %#v want %#v", pin.Tools, tc.want)
			}
			if pin.Definition.AllowTools != nil {
				t.Fatal("selection became a permission rule")
			}
		})
	}
}

func TestWorkerPolicyRecovery(t *testing.T) {
	e := &Executor{}
	parent := []string{"Read", "Skill"}
	e.SetParentTools(func() []string { return parent })
	config := &AgentConfig{Name: "review", AllowTools: ToolList{{Name: "Read"}, {Name: "Skill"}}, SystemPrompt: "original"}
	req := tool.AgentExecRequest{Tools: []string{"Read"}, ResolvedAgentConfig: config}
	pin, err := e.PinWorkerPolicy(req)
	if err != nil {
		t.Fatal(err)
	}
	config.SystemPrompt = "changed"
	config.AllowTools[0].Name = "Write"
	req.Tools[0] = "Skill"
	data, err := json.Marshal(pin)
	if err != nil {
		t.Fatal(err)
	}
	var restored WorkerPolicy
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][]string{{"Read", "Skill", "Write"}, {"Skill"}, nil} {
		parent = p
		cfg, err := e.prepareRunConfig(context.Background(), tool.AgentExecRequest{ResolvedAgentConfig: &restored})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.config.SystemPrompt != "original" || !cfg.config.AllowTools.HasName("Read") {
			t.Fatal("mutable definition")
		}
		if cfg.ceiling == nil || slices.Contains(cfg.ceiling, "Skill") || slices.Contains(cfg.ceiling, "Read") != slices.Contains(p, "Read") {
			t.Fatalf("restored ceiling %#v with parent %#v", cfg.ceiling, p)
		}
	}
}

func TestFlowFingerprintIncludesArgumentRulesAndMode(t *testing.T) {
	a := NewExecutorAdapter(&Executor{})
	def := &AgentConfig{AllowTools: ToolList{{Name: "Bash", Pattern: "git diff*"}}}
	req := tool.AgentExecRequest{ResolvedAgentConfig: def, Tools: []string{"Bash"}}
	pin, first, err := a.PinFlowWorker(req)
	if err != nil {
		t.Fatal(err)
	}
	def.AllowTools[0].Pattern = "git *"
	_, second, err := a.PinFlowWorker(req)
	if err != nil || first == second {
		t.Fatalf("changed Bash rule: %q, %q, %v", first, second, err)
	}
	a.SetParentPermissionMode(func() PermissionMode { return PermissionBypass })
	req.ResolvedAgentConfig = pin
	if _, err := a.PinWorkerPolicy(req); err == nil {
		t.Fatal("approval survived an inherited permission mode change")
	}
}

func TestWorkerEmptyAllowToolsYAML(t *testing.T) {
	for _, src := range []string{"allow_tools: []", "allow_tools: {}", "allow_tools: ''"} {
		var def AgentConfig
		if err := yaml.Unmarshal([]byte(src), &def); err != nil {
			t.Fatal(err)
		}
		if def.AllowTools == nil {
			t.Fatalf("%s inherited tools", src)
		}
		pin, err := new(Executor).PinWorkerPolicy(tool.AgentExecRequest{ResolvedAgentConfig: &def})
		if err != nil || pin.Tools == nil || len(pin.Tools) != 0 {
			t.Fatalf("%s: %+v, %v", src, pin, err)
		}
	}
}
