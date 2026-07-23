// Package subagent provides isolated LLM workers spawned by the Agent tool.
package subagent

import (
	"strings"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

// PermissionMode controls the default permission policy for an agent.
// See docs/concepts/permission-model.md for the full pipeline.
type PermissionMode string

const (
	// PermissionDefault: reads auto, mutations Ask. In a subagent Ask
	// collapses to Deny, so mutations are blocked.
	PermissionDefault PermissionMode = "default"

	// PermissionAcceptEdits: reads + edit/write auto, Bash/exec/agent Ask.
	PermissionAcceptEdits PermissionMode = "acceptEdits"

	// PermissionExplore: reads auto, mutations explicitly Deny (never Ask).
	// Used for research and read-only investigation.
	PermissionExplore PermissionMode = "explore"

	// PermissionBypass: everything Allow after the root/home-removal circuit
	// breaker and parent-only tool boundary, which still gate the call.
	PermissionBypass PermissionMode = "bypassPermissions"

	// PermissionDontAsk: reads auto, everything else silently Deny.
	// TODO: not yet wired into the main loop pipeline; behaves as
	// PermissionDefault in subagent context (Ask -> Deny is automatic).
	PermissionDontAsk PermissionMode = "dontAsk"

	// PermissionAuto: long-running subagent mode. Auto-approves more than
	// acceptEdits, including benign Bash, with a safety classifier on the
	// rest. TODO: classifier not implemented; treated as PermissionAcceptEdits.
	PermissionAuto PermissionMode = "auto"
)

// NormalizePermissionMode trims a runtime permission mode and folds internal
// aliases onto canonical constants. The Agent tool validates its narrower
// model-facing enum before execution.
func NormalizePermissionMode(s string) PermissionMode {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "":
		return PermissionDefault
	case "edit", "acceptedits":
		return PermissionAcceptEdits
	case "explore", "readonly", "read-only":
		return PermissionExplore
	case "bypass", "bypasspermissions":
		return PermissionBypass
	case "dontask", "dont-ask":
		return PermissionDontAsk
	case "auto":
		return PermissionAuto
	case "default":
		return PermissionDefault
	}
	return PermissionMode(s)
}

const (
	defaultAgentName        = "subagent"
	defaultAgentDescription = "General-purpose subagent for research and implementation tasks."

	// defaultMaxSteps is both the default and minimum number of LLM inference steps.
	defaultMaxSteps = 500
)

// AgentResult contains the result of an agent execution.
type AgentResult struct {
	AgentID        string
	AgentName      string
	Model          string
	Success        bool
	Content        string
	TranscriptPath string
	Messages       []core.Message
	StepCount      int
	ToolUses       int
	TokenUsage     llm.Usage
	Duration       time.Duration
	Activity       []string
	Error          string
}

// modelAliases maps short model aliases to current-generation model ids.
// Un-dated ids resolve on Anthropic-family providers (API and Vertex both
// serve these prefixes); a miss falls back to the parent model via
// shouldRetryWithParentModel.
var modelAliases = map[string]string{
	"sonnet": "claude-sonnet-4-6",
	"opus":   "claude-opus-4-7",
	"haiku":  "claude-haiku-4-5",
}

// resolveModelAlias returns the full model ID for a known alias,
// or the input unchanged if it is not an alias.
func resolveModelAlias(model string) string {
	if full, ok := modelAliases[model]; ok {
		return full
	}
	return model
}
