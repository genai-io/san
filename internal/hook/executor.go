package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/genai-io/san/internal/setting"
)

func (e *Engine) executeMatchedHook(ctx context.Context, hook matchedHook, input HookInput) HookOutcome {
	statusID := e.status.Start(matchedHookStatusMessage(hook))
	defer e.status.End(statusID)

	if hook.Func != nil {
		return e.executeFunctionHook(ctx, *hook.Func, input)
	}
	if hook.Command == nil {
		return HookOutcome{
			ShouldContinue: true,
			Error:          fmt.Errorf("hook has no executable payload"),
		}
	}

	switch normalizedHookType(*hook.Command) {
	case "command":
		if e.getPromptCallback() != nil {
			return e.executeCommandBidirectional(ctx, *hook.Command, input)
		}
		return e.executeCommand(ctx, *hook.Command, input)
	case "prompt":
		return e.executePromptHook(ctx, *hook.Command, input)
	case "agent":
		return e.executeAgentHook(ctx, *hook.Command, input)
	case "http":
		return e.executeHTTPHook(ctx, *hook.Command, input)
	default:
		return HookOutcome{
			ShouldContinue: true,
			Error:          fmt.Errorf("unsupported hook type: %s", hook.Command.Type),
		}
	}
}

func (e *Engine) executeDetachedHook(ctx context.Context, hook matchedHook, input HookInput) {
	start := time.Now()
	result := e.executeMatchedHook(ctx, hook, input)
	e.audit(HookFiredAudit{
		Event:    string(hook.Event),
		Source:   matchedHookIdentity(hook),
		Matcher:  hook.Matcher,
		Outcome:  outcomeOf(result),
		Reason:   reasonOf(result),
		Duration: time.Since(start),
	})
	if hook.Command == nil || !hook.Command.AsyncRewake || !result.ShouldBlock {
		return
	}
	cb := e.getAsyncHookCallback()
	if cb == nil {
		return
	}
	cb(AsyncHookResult{
		Event:       hook.Event,
		HookType:    matchedHookType(hook),
		HookSource:  hook.Source,
		HookName:    matchedHookIdentity(hook),
		BlockReason: result.BlockReason,
	})
}

func (e *Engine) mergeOutcome(outcome, result HookOutcome) HookOutcome {
	outcome.AdditionalContext = appendContext(outcome.AdditionalContext, result.AdditionalContext)
	if result.UpdatedInput != nil {
		outcome.UpdatedInput = result.UpdatedInput
	}
	if result.ForceAsk {
		outcome.ForceAsk = true
	}
	if result.PermissionAllow {
		outcome.PermissionAllow = true
		outcome.HookSource = result.HookSource
	}
	if len(result.UpdatedPermissions) > 0 {
		outcome.UpdatedPermissions = append(outcome.UpdatedPermissions, result.UpdatedPermissions...)
	}
	if len(result.WatchPaths) > 0 {
		outcome.WatchPaths = append(outcome.WatchPaths, result.WatchPaths...)
	}
	if result.InitialUserMessage != "" {
		outcome.InitialUserMessage = result.InitialUserMessage
	}
	if result.Retry {
		outcome.Retry = true
	}
	return outcome
}

func (e *Engine) buildEnv(ctx context.Context, input HookInput) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := append(os.Environ(),
		setting.EnvPairs(
			"PROJECT_DIR", e.cwd,
			"SESSION_ID", e.sessionID,
			"EVENT_TYPE", input.HookEventName,
		)...,
	)
	if input.ToolName != "" {
		result = append(result, setting.EnvPair("TOOL_NAME", input.ToolName)...)
	}
	if fn := e.envProvider; fn != nil {
		result = append(result, fn(ctx)...)
	}
	return result
}

func getExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func (e *Engine) parseOutput(output string, outcome HookOutcome) HookOutcome {
	if output == "" {
		return outcome
	}

	var hookOutput HookOutput
	if err := json.Unmarshal([]byte(output), &hookOutput); err != nil {
		outcome.Error = fmt.Errorf("hook output not valid JSON: %w", err)
		return outcome
	}
	return e.applyHookOutput(outcome, hookOutput)
}

func (e *Engine) applyHookOutput(outcome HookOutcome, hookOutput HookOutput) HookOutcome {
	if hookOutput.Continue != nil && !*hookOutput.Continue {
		outcome.ShouldContinue = false
		outcome.ShouldBlock = true
		outcome.BlockReason = firstNonEmpty(hookOutput.StopReason, hookOutput.Reason)
	}

	if hookOutput.SystemMessage != "" {
		outcome.AdditionalContext = hookOutput.SystemMessage
	}

	if hso := hookOutput.HookSpecificOutput; hso != nil {
		outcome = e.applySpecificOutput(outcome, hso)
	}

	return outcome
}

func (e *Engine) executeFunctionHook(ctx context.Context, hook FunctionHook, input HookInput) HookOutcome {
	outcome := HookOutcome{ShouldContinue: true}
	if hook.Callback == nil {
		return HookOutcome{
			ShouldContinue: true,
			Error:          fmt.Errorf("function hook %q has no callback", hook.ID),
		}
	}

	timeout := defaultTimeout
	if hook.Timeout > 0 {
		timeout = hook.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	output, err := hook.Callback(ctx, input)
	if err != nil {
		outcome.Error = err
		return outcome
	}
	return e.applyHookOutput(outcome, output)
}

func (e *Engine) applySpecificOutput(outcome HookOutcome, hso *HookSpecificOutput) HookOutcome {
	switch hso.PermissionDecision {
	case "deny":
		outcome.ShouldContinue = false
		outcome.ShouldBlock = true
		outcome.BlockReason = hso.PermissionDecisionReason
	case "allow":
		outcome.PermissionAllow = true
		outcome.HookSource = "PreToolUse"
	case "ask":
		outcome.ForceAsk = true
	}

	if hso.UpdatedInput != nil {
		outcome.UpdatedInput = hso.UpdatedInput
	}

	outcome.AdditionalContext = appendContext(outcome.AdditionalContext, hso.AdditionalContext)
	if len(hso.WatchPaths) > 0 {
		outcome.WatchPaths = append([]string(nil), hso.WatchPaths...)
	}
	if hso.InitialUserMessage != "" {
		outcome.InitialUserMessage = hso.InitialUserMessage
	}
	if hso.Retry && hso.HookEventName == "PermissionDenied" {
		outcome.Retry = true
	}

	if prd := hso.PermissionRequestDecision; prd != nil && hso.HookEventName == "PermissionRequest" {
		outcome = e.applyPermissionDecision(outcome, prd)
	}

	return outcome
}

func (e *Engine) applyPermissionDecision(outcome HookOutcome, prd *PermissionRequestDecision) HookOutcome {
	switch prd.Behavior {
	case "deny":
		outcome.ShouldContinue = false
		outcome.ShouldBlock = true
		outcome.BlockReason = "denied by hook"
		if prd.Message != "" {
			outcome.BlockReason = prd.Message
		}
	case "allow":
		outcome.PermissionAllow = true
		outcome.HookSource = "PermissionRequest"
		if prd.UpdatedInput != nil {
			outcome.UpdatedInput = prd.UpdatedInput
		}
		for _, p := range prd.UpdatedPermissions {
			if pu := parsePermissionUpdate(p); pu.Type != "" {
				outcome.UpdatedPermissions = append(outcome.UpdatedPermissions, pu)
			}
		}
		return outcome
	}

	if prd.Interrupt {
		outcome.ShouldContinue = false
		outcome.ShouldBlock = true
	}
	if prd.UpdatedInput != nil {
		outcome.UpdatedInput = prd.UpdatedInput
	}
	return outcome
}

func appendContext(a, b string) string {
	if b == "" {
		return a
	}
	if a == "" {
		return b
	}
	return a + "\n" + b
}

func firstNonEmpty(strs ...string) string {
	for _, s := range strs {
		if s != "" {
			return s
		}
	}
	return ""
}

func parsePermissionUpdate(v any) PermissionUpdate {
	m, ok := v.(map[string]any)
	if !ok {
		if s, ok := v.(string); ok && s != "" {
			return PermissionUpdate{
				Type:        "addRules",
				Behavior:    "allow",
				Destination: "session",
				Rules:       []PermissionRule{{RuleContent: s}},
			}
		}
		return PermissionUpdate{}
	}

	pu := PermissionUpdate{
		Type:        getString(m, "type"),
		Mode:        getString(m, "mode"),
		Behavior:    getString(m, "behavior"),
		Destination: getString(m, "destination"),
	}
	if rules, ok := m["rules"].([]any); ok {
		for _, r := range rules {
			if rm, ok := r.(map[string]any); ok {
				pu.Rules = append(pu.Rules, PermissionRule{
					ToolName:    getString(rm, "toolName"),
					RuleContent: getString(rm, "ruleContent"),
				})
			}
		}
	}
	if dirs, ok := m["directories"].([]any); ok {
		for _, d := range dirs {
			if s, ok := d.(string); ok {
				pu.Directories = append(pu.Directories, s)
			}
		}
	}
	return pu
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func (e *Engine) getCwd() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cwd
}

func (e *Engine) getPromptCallback() PromptCallback {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.promptCallback
}

func (e *Engine) getLLMCompleter() LLMCompleter {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.llmCompleter
}

func (e *Engine) resolveModel(cmd setting.HookCmd) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if cmd.Model != "" {
		return cmd.Model
	}
	return e.hookModel
}

func buildHookPrompt(template string, inputJSON string) string {
	if strings.Contains(template, "$ARGUMENTS") {
		return strings.ReplaceAll(template, "$ARGUMENTS", inputJSON)
	}
	return strings.TrimSpace(template + "\n\nHook input JSON:\n" + inputJSON)
}
