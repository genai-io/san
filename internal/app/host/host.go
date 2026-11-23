package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// ReconcileMCPTools syncs a tool set with the live MCP catalog, minus disabled tools.
func ReconcileMCPTools(tools *core.Tools, catalog []core.Tool, disabled map[string]bool) {
	live := make(map[string]bool)
	for _, t := range catalog {
		name := t.Schema().Name
		if disabled[name] {
			continue
		}
		live[name] = true
		tools.Add(t, "mcp:"+name)
	}
	for _, schema := range tools.Schemas() {
		if mcp.IsMCPTool(schema.Name) && !live[schema.Name] {
			tools.Remove(schema.Name, "mcp:"+schema.Name)
		}
	}
}

// ValidateResumeDirectory rejects implicit execution-directory changes; it
// neither grants trust nor changes the current process working directory.
func ValidateResumeDirectory(saved, requested string) error {
	if saved == "" {
		return nil
	}
	canonical := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(absolute)
	}
	persisted, err := canonical(saved)
	if err != nil {
		return fmt.Errorf("cannot validate saved session directory %q: %w; restore that directory before resuming", saved, err)
	}
	current, err := canonical(requested)
	if err != nil {
		return fmt.Errorf("cannot validate requested session directory %q: %w", requested, err)
	}
	if persisted != current {
		return fmt.Errorf("session directory mismatch: saved directory %q differs from requested directory %q; reopen San in the saved directory to resume (implicit directory switching is not supported)", saved, requested)
	}
	return nil
}

// SessionTaskDir holds a session's tracker items and task outputs.
func SessionTaskDir(homeDir, id string) string {
	return filepath.Join(confdir.Dir(homeDir), "tasks", id)
}

// BindSessionHooks captures the native owner; child engines must not reuse a
// parent's completion recorder or model-completion closure.
func BindSessionHooks(engine *hook.Engine, provider llm.Provider, owner *session.Session, model string) {
	if engine == nil {
		return
	}
	engine.SetAuditCallback(nil)
	engine.SetLLMCompleter(nil, model)
	engine.SetRecordedLLMCompleter(RecordedHookCompleter(provider, owner), model)
	engine.SetCompletionRecorder(session.NewRecorder(owner, "").RecordHook)
}

// RecordedHookCompleter runs hook model calls as recorded completions of owner.
func RecordedHookCompleter(p llm.Provider, owner *session.Session) hook.RecordedLLMCompleter {
	if p == nil {
		return nil
	}
	return func(ctx context.Context, systemPrompt, userMessage, model string) (hook.LLMCompletion, error) {
		scope := owner.Completion(session.PurposeHook)
		c := llm.NewClient(p, model, 0)
		resp, err := c.Complete(ctx, systemPrompt, []core.Message{core.UserMessage(userMessage, nil)}, nil, 4096, scope.Admission())
		result := hook.LLMCompletion{Text: resp.Content.Text(), Finish: scope.FinishHook}
		var admission *ai.AdmissionError
		var recording *llm.RecordingError
		if errors.As(err, &admission) || errors.As(err, &recording) {
			err = &hook.RecordingError{Err: err}
		}
		return result, err
	}
}

// WarnShellFallback reports on stderr when the preferred shell is unavailable.
func WarnShellFallback() {
	if shell, err := proc.DefaultShell(); err == nil && shell.FallbackReason != "" {
		fmt.Fprintln(os.Stderr, "san: "+shell.FallbackReason)
	}
}

// ResolveProvider connects to the best available provider for a one-shot,
// non-interactive run (print mode, headless agent) and returns it with a
// concrete model id. It shares llm.ResolveProvider's resolution order with the
// interactive startup path; when resolution falls back to a connection without
// a saved current model, it fills that provider's default model id.
func ResolveProvider(ctx context.Context) (llm.Provider, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	store, err := llm.NewStoreForCwd(cwd)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load store: %w", err)
	}
	resolved, ok := llm.ResolveProvider(ctx, store)
	if !ok {
		return nil, "", fmt.Errorf("no provider connected. Run 'san' and use /models to connect")
	}
	modelID := resolved.ModelID
	if modelID == "" {
		// Provider.Name() carries the "name:auth" form; DefaultModel keys on the
		// bare provider name.
		name, _, _ := strings.Cut(resolved.Provider.Name(), ":")
		modelID = setting.DefaultModel(name, string(resolved.AuthMethod))
	}
	if modelID == "" {
		// Providers without a built-in default model (e.g. a custom provider)
		// need an explicit selection.
		return nil, "", fmt.Errorf("no model selected for %s. Run 'san' and use /models to select one", resolved.Provider.Name())
	}
	return resolved.Provider, modelID, nil
}
