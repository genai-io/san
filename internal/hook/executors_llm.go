package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/genai-io/san/internal/setting"
)

const (
	hookJSONResponseInstruction = "Return exactly one JSON object matching the hook output schema. Do not include markdown fences or commentary."
	defaultLLMHookTimeout       = 5 * time.Minute
)

func (e *Engine) executePromptHook(ctx context.Context, hookCmd setting.HookCmd, input HookInput) HookOutcome {
	outcome := HookOutcome{ShouldContinue: true}

	// Add timeout if context has no deadline (e.g., detached hooks with context.Background)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultLLMHookTimeout)
		defer cancel()
	}

	completer := e.getLLMCompleter()
	model := e.resolveModel(hookCmd)
	if completer == nil || model == "" {
		outcome.Error = fmt.Errorf("prompt hook requires an active provider and model")
		return outcome
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		outcome.Error = fmt.Errorf("failed to marshal input: %w", err)
		return outcome
	}

	resp, err := completer(ctx, hookJSONResponseInstruction, buildHookPrompt(hookCmd.Prompt, string(inputJSON)), model)
	if err != nil {
		outcome.Error = err
		return outcome
	}

	return e.parseOutput(strings.TrimSpace(resp), outcome)
}

func (e *Engine) executeAgentHook(ctx context.Context, hookCmd setting.HookCmd, input HookInput) HookOutcome {
	outcome := HookOutcome{ShouldContinue: true}

	// Add timeout if context has no deadline (e.g., detached hooks with context.Background)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultLLMHookTimeout)
		defer cancel()
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		outcome.Error = fmt.Errorf("failed to marshal input: %w", err)
		return outcome
	}

	model := e.resolveModel(hookCmd)
	prompt := buildHookPrompt(hookCmd.Prompt, string(inputJSON))

	completer := e.getLLMCompleter()
	if completer == nil || model == "" {
		outcome.Error = fmt.Errorf("agent hook requires an active provider and model")
		return outcome
	}

	systemPrompt := "You are an autonomous hook verifier. Reason through the request and return only hook JSON.\n" + hookJSONResponseInstruction
	resp, err := completer(ctx, systemPrompt, prompt, model)
	if err != nil {
		outcome.Error = err
		return outcome
	}
	return e.parseOutput(strings.TrimSpace(resp), outcome)
}
