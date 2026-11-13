package input

import (
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
)

const contextLimitUsage = "Usage:\n  /context limit <window> <output>  override the current model's context window and max output\n  /context limit reset              drop the override"

// ContextLimitUpdatedMsg binds an explicit successful change to its model and original owner.
type ContextLimitUpdatedMsg struct {
	Provider  string
	ModelID   string
	Session   *session.Session
	MaxTokens int
}

// setContextLimit overrides the current model's window and output cap by hand,
// for a model whose provider does not publish them or gets them wrong. The
// figures are the vendor's own: the whole window, and the most one reply may
// produce.
func setContextLimit(store *llm.Store, current *llm.CurrentModelInfo, args string) (string, *ContextLimitUpdatedMsg) {
	if store == nil || current == nil {
		return "No model selected. Use /models to select a model first.", nil
	}
	args = strings.TrimSpace(args)
	if args == "reset" {
		if err := store.ClearContextLimit(current.ModelID); err != nil {
			return "Error: " + err.Error(), nil
		}
		return "Cleared the context limit override for " + current.ModelID + ".", &ContextLimitUpdatedMsg{Provider: string(current.Provider), ModelID: current.ModelID, MaxTokens: kit.GetMaxOutput(store, current, setting.DefaultMaxTokens)}
	}

	var window, output int
	if _, err := fmt.Sscanf(args, "%d %d", &window, &output); err != nil || window <= 0 || output <= 0 {
		return contextLimitUsage, nil
	}
	if output >= window {
		return "The max output must be smaller than the context window, which it is part of.", nil
	}
	if err := store.SetContextLimit(current.ModelID, window, output); err != nil {
		return "Error: " + err.Error(), nil
	}
	return fmt.Sprintf("%s: window %s · max output %s · auto-compacts at %s",
		current.ModelID, kit.FormatTokenCount(window), kit.FormatTokenCount(output),
		kit.FormatTokenCount(llm.ContextBudget(window, output))), &ContextLimitUpdatedMsg{Provider: string(current.Provider), ModelID: current.ModelID, MaxTokens: output}
}
