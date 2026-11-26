package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	agentpkg "github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
)

// Print streams one response and shuts down tasks started by the turn.
func Print(userMessage, personaName string) (err error) {
	defer func() {
		if stopErr := task.Default().Shutdown(); stopErr != nil {
			err = errors.Join(err, stopErr)
		}
	}()
	ctx := context.Background()

	llmProvider, modelID, err := llm.ResolveProviderModel(ctx)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	warnUntrustedWorkspace(cwd)
	params, err := buildPrintParams(cwd, personaName)
	if err != nil {
		return err
	}
	if personaName != "" && params.ModelID != "" {
		if models, err := llmProvider.ListModels(ctx); err == nil {
			for _, m := range models {
				if m.ID == params.ModelID {
					modelID = params.ModelID
					break
				}
			}
		}
	}
	params.Provider, params.ModelID = llmProvider, modelID
	res, err := runRecordedPrint(ctx, params, core.UserMessage(userMessage, nil), func(text string) { fmt.Print(text) })
	if err != nil {
		return err
	}
	// A turn that ended on tools alone streamed nothing; say what it settled on
	// rather than exiting silent, which is what this path used to do on every
	// tool call.
	if res != nil && strings.TrimSpace(res.Content) == "" {
		fmt.Print(res.Content)
	}
	fmt.Println()
	return nil
}

func buildPrintParams(cwd, personaName string) (agentpkg.BuildParams, error) {
	settings, err := setting.LoadForCwd(cwd)
	if err != nil {
		return agentpkg.BuildParams{}, err
	}
	var parts system.Persona
	if personaName != "" {
		p, ok := persona.Default().Get(personaName)
		if !ok {
			return agentpkg.BuildParams{}, fmt.Errorf("unknown persona %q", personaName)
		}
		if p.Settings != nil {
			settings = setting.ApplyPersonaOverlay(settings, &p.Settings.Data)
		}
		parts = system.Persona{Identity: p.Identity, Behavior: p.Behavior, Rules: p.Rules}
	}
	permissions := &setting.SessionPermissions{ShouldAvoidPrompts: true}
	var memory system.MemoryStore
	if setting.New(settings).AutoMemory() {
		memory = setting.MemoryStoreFor(cwd)
	}
	policy := agentpkg.PermissionPolicy{Settings: settings, Session: permissions}
	return agentpkg.BuildParams{
		AutoMemory:    agentpkg.AutoMemoryConfig{Store: memory, Recall: settings.MemoryRecall(), IndexLimits: settings.MemoryPreview()},
		ModelID:       settings.Model,
		MaxTokens:     setting.DefaultMaxTokens,
		CWD:           cwd,
		Persona:       parts,
		DisabledTools: setting.WithDefaultDisabledTools(settings.DisabledTools),
		// Headless uses the same effective policy; prompts become refusals.
		PermissionRules:  policy.Decide,
		CanApproveByHook: policy.CanApproveByHook,
	}, nil
}
