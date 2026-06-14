package setting

import (
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/genai-io/san/internal/tool/perm"
)

// PermissionBehavior is the outcome of a permission check. It aliases
// perm.Decision so the gate, the agent runtime, and subagents all speak a
// single decision type. Allow/Deny/Ask are kept as thin synonyms for
// perm.Permit/Reject/Prompt while the permission engine is being unified.
type PermissionBehavior = perm.Decision

const (
	Allow = perm.Permit // automatically allowed
	Deny  = perm.Reject // automatically denied
	Ask   = perm.Prompt // requires user confirmation (prompt)
)

// PermissionDecision carries a permission behavior together with the reason
// for the decision, enabling callers to log or display why access was
// granted, denied, or requires confirmation.
type PermissionDecision struct {
	Behavior PermissionBehavior
	Reason   string // e.g. "deny rule: Read(**/.env)", "bypass-immune: .git/ directory"
}

// decide is a shorthand for building a PermissionDecision.
func decide(b PermissionBehavior, reason string) PermissionDecision {
	return PermissionDecision{Behavior: b, Reason: reason}
}

// ---------------------------------------------------------------------------
// Main permission gate — HasPermissionToUseTool
// ---------------------------------------------------------------------------
//
// Decision pipeline (inspired by Claude Code's hasPermissionsToUseTool):
//
//  1. Deny rules
//  2. Bypass-immune safety checks
//  3. BypassPermissions mode → allow (everything except steps 1-2)
//  4. Session permissions (runtime overrides)
//  5. Ask rules
//  6. Allow rules
//  7. Mode default
//  8. Headless / DontAsk coercion: Ask → Deny

// HasPermissionToUseTool is the central permission gate that determines
// whether a tool invocation should be allowed, denied, or prompted.
func (s *Data) HasPermissionToUseTool(toolName string, args map[string]any, session *SessionPermissions) PermissionDecision {
	rule := BuildRule(toolName, args)

	// ── Step 1: Deny rules ──
	for _, pattern := range s.Permissions.Deny {
		if MatchesToolPattern(toolName, args, rule, pattern) {
			return decide(Deny, "deny rule: "+pattern)
		}
	}

	// ── Step 2: Bypass-immune safety checks ──
	if reason := s.bypassImmunePromptReason(toolName, args, session); reason != "" {
		return coerceAsk(decide(Ask, reason), session)
	}

	// ── Step 3: BypassPermissions mode ──
	if session != nil && session.Mode == ModeBypassPermissions {
		return decide(Allow, "mode: bypass permissions")
	}

	// ── Step 4: Session permissions ──
	if session != nil {
		if session.IsToolAllowed(toolName) {
			return decide(Allow, "session: allow all "+toolName)
		}
		if pattern, ok := MatchAllowList(toolName, args, slices.Collect(maps.Keys(session.AllowedPatterns))); ok {
			return decide(Allow, "session pattern: "+pattern)
		}
	}

	// ── Step 5: Ask rules ──
	for _, pattern := range s.Permissions.Ask {
		if MatchesToolPattern(toolName, args, rule, pattern) {
			return coerceAsk(decide(Ask, "ask rule: "+pattern), session)
		}
	}

	// ── Step 6: Allow rules ──
	if pattern, ok := MatchAllowList(toolName, args, s.Permissions.Allow); ok {
		return decide(Allow, "allow rule: "+pattern)
	}

	// ── Step 7/8: Mode default + headless coercion ──
	return coerceAsk(modeDefaultDecision(toolName, session), session)
}

func (s *Data) bypassImmunePromptReason(toolName string, args map[string]any, session *SessionPermissions) string {
	if reason := BypassImmuneReason(toolName, args); reason != "" {
		return "bypass-immune: " + reason
	}

	if session != nil && len(session.WorkingDirectories) > 0 {
		if toolName == "Edit" || toolName == "Write" {
			if fp, ok := args["file_path"].(string); ok {
				if !isInWorkingDirectory(fp, session.WorkingDirectories) {
					return "outside working directory"
				}
			}
		}
	}
	return ""
}

// ModeDefault is step 7 of the permission pipeline: the decision for a tool
// when no deny/ask/allow rule matched, decided by the mode alone. Safe tools are
// always permitted; the rest depends on the mode. Exported so the subagent gate
// shares this one mode table instead of carrying its own (perm.Checker).
func ModeDefault(toolName string, mode OperationMode) PermissionDecision {
	if perm.IsSafeTool(toolName) {
		return decide(Allow, "mode: safe tool")
	}

	switch mode {
	case ModeBypassPermissions:
		return decide(Allow, "mode: bypass permissions")
	case ModeAutoAccept:
		if perm.IsEditTool(toolName) {
			return decide(Allow, "mode: accept edits")
		}
		return decide(Ask, "mode: accept edits requires confirmation")
	case ModeReadOnly:
		return decide(Deny, "mode: read-only")
	case ModeDontAsk:
		return decide(Deny, "mode: don't ask (auto-deny)")
	default: // ModeNormal
		return decide(Ask, "mode: default requires confirmation")
	}
}

func modeDefaultDecision(toolName string, session *SessionPermissions) PermissionDecision {
	mode := ModeNormal
	if session != nil {
		mode = session.Mode
	}
	return ModeDefault(toolName, mode)
}

func coerceAsk(decision PermissionDecision, session *SessionPermissions) PermissionDecision {
	if decision.Behavior != Ask || session == nil {
		return decision
	}
	if session.Mode == ModeDontAsk {
		return decide(Deny, "mode: don't ask (auto-deny): "+decision.Reason)
	}
	if session.ShouldAvoidPrompts {
		return decide(Deny, "headless: "+decision.Reason)
	}
	return decision
}

// ResolveHookAllow checks if a hook's "allow" decision should be honored.
// Returns false if a deny rule, bypass-immune safety check, or explicit ask
// rule overrides the hook's decision. This implements the safety invariant:
// deny rules > bypass-immune checks > ask rules > hook allow.
func (s *Data) ResolveHookAllow(toolName string, args map[string]any, session *SessionPermissions) bool {
	rule := BuildRule(toolName, args)
	for _, pattern := range s.Permissions.Deny {
		if MatchesToolPattern(toolName, args, rule, pattern) {
			return false
		}
	}
	if s.bypassImmunePromptReason(toolName, args, session) != "" {
		return false
	}
	for _, pattern := range s.Permissions.Ask {
		if MatchesToolPattern(toolName, args, rule, pattern) {
			return false
		}
	}
	return true
}

// CheckPermission is a convenience wrapper returning just the behavior.
func (s *Data) CheckPermission(toolName string, args map[string]any, session *SessionPermissions) PermissionBehavior {
	return s.HasPermissionToUseTool(toolName, args, session).Behavior
}

// BuildRule builds a rule string from a tool name and arguments.
// Format: "Tool(args)"
//
// Different tools extract different parts of args:
//   - Bash: "Bash(command)" where command is the shell command
//   - Read/Edit/Write: "Read(file_path)"
//   - Glob/Grep: "Glob(pattern)" or "Grep(pattern)"
//   - WebFetch: "WebFetch(domain:hostname)"
func BuildRule(toolName string, args map[string]any) string {
	var argStr string

	switch toolName {
	case "Bash":
		// For Bash, use the command with prefix matching support
		if cmd, ok := args["command"].(string); ok {
			// Prefer the first meaningful subcommand so compound commands like
			// "cd repo && git status" build reusable rules.
			if normalized := meaningfulBashCommands(cmd); len(normalized) > 0 {
				argStr = normalized[0]
			} else {
				argStr = normalizeBashCommand(cmd)
			}
		}

	case "Read", "Edit", "Write":
		// For file tools, use the file path
		if fp, ok := args["file_path"].(string); ok {
			argStr = fp
		}

	case "Glob":
		// For Glob, use the pattern
		if p, ok := args["pattern"].(string); ok {
			argStr = p
		}

	case "Grep":
		// For Grep, use the pattern
		if p, ok := args["pattern"].(string); ok {
			argStr = p
		}

	case "WebFetch":
		// For WebFetch, extract domain from URL
		if u, ok := args["url"].(string); ok {
			if parsed, err := url.Parse(u); err == nil {
				argStr = "domain:" + parsed.Host
			} else {
				argStr = u
			}
		}

	case "Skill":
		// For Skill, use the skill name
		// Supports patterns like "Skill(git:*)", "Skill(test-skill)"
		if s, ok := args["skill"].(string); ok {
			argStr = s
		}

	default:
		// Generic: try common field names
		if fp, ok := args["file_path"].(string); ok {
			argStr = fp
		} else if p, ok := args["path"].(string); ok {
			argStr = p
		} else if p, ok := args["pattern"].(string); ok {
			argStr = p
		}
	}

	return toolName + "(" + argStr + ")"
}

// normalizeBashCommand normalizes a bash command for pattern matching.
// Examples:
//   - "npm install lodash" -> "npm:install lodash"
//   - "git commit -m 'msg'" -> "git:commit -m 'msg'"
//   - "ls -la" -> "ls:-la"
//   - "/bin/rm -rf foo" -> "rm:-rf foo" (strips path prefix)
func normalizeBashCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	parts := strings.SplitN(cmd, " ", 2)

	// Get the base command (without path)
	baseCmd := filepath.Base(parts[0])

	if len(parts) == 1 {
		return baseCmd
	}

	// Return "command:rest"
	return baseCmd + ":" + parts[1]
}

func normalizeParsedCommand(cmd parsedCommand) string {
	if cmd.Name == "" {
		return ""
	}
	if len(cmd.Args) == 0 {
		return cmd.Name
	}
	return cmd.Name + ":" + strings.Join(cmd.Args, " ")
}

func normalizedBashCommands(cmd string) []string {
	if file := parseBashAST(cmd); file != nil {
		parsed := extractCommandsAST(file)
		normalized := make([]string, 0, len(parsed))
		for _, subCmd := range parsed {
			if n := normalizeParsedCommand(subCmd); n != "" {
				normalized = append(normalized, n)
			}
		}
		if len(normalized) > 0 {
			return normalized
		}
	}

	rawCommands := extractBashCommands(cmd)
	normalized := make([]string, 0, len(rawCommands))
	for _, subCmd := range rawCommands {
		if n := normalizeBashCommand(subCmd); n != "" {
			normalized = append(normalized, n)
		}
	}
	return normalized
}

func meaningfulBashCommands(cmd string) []string {
	normalized := normalizedBashCommands(cmd)
	if len(normalized) <= 1 {
		return normalized
	}

	filtered := make([]string, 0, len(normalized))
	for _, subCmd := range normalized {
		if subCmd == "cd" || strings.HasPrefix(subCmd, "cd:") {
			continue
		}
		filtered = append(filtered, subCmd)
	}
	if len(filtered) > 0 {
		return filtered
	}
	return normalized
}

func bashRuleForms(cmd string) []string {
	forms := make([]string, 0, 8)
	seen := make(map[string]bool)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		forms = append(forms, s)
	}

	for _, raw := range extractBashCommands(cmd) {
		add(raw)
	}
	for _, normalized := range normalizedBashCommands(cmd) {
		add(normalized)
	}
	return forms
}

func bashSubcommandRuleForms(cmd string) [][]string {
	var parsed []parsedCommand
	if file := parseBashAST(cmd); file != nil {
		parsed = extractCommandsAST(file)
	}

	if len(parsed) > 0 {
		result := make([][]string, 0, len(parsed))
		for _, subCmd := range parsed {
			forms := []string{subCmd.String()}
			if normalized := normalizeParsedCommand(subCmd); normalized != "" && normalized != subCmd.String() {
				forms = append(forms, normalized)
			}
			result = append(result, forms)
		}
		return result
	}

	rawCommands := extractBashCommands(cmd)
	result := make([][]string, 0, len(rawCommands))
	for _, raw := range rawCommands {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		forms := []string{raw}
		if normalized := normalizeBashCommand(raw); normalized != "" && normalized != raw {
			forms = append(forms, normalized)
		}
		result = append(result, forms)
	}
	return result
}

// extractBashCommands extracts individual commands from a chained bash command.
// It splits on && and ; to get each command separately.
func extractBashCommands(cmd string) []string {
	var commands []string

	// Split on && first, then on ;
	for part := range strings.SplitSeq(cmd, "&&") {
		for subPart := range strings.SplitSeq(part, ";") {
			trimmed := strings.TrimSpace(subPart)
			if trimmed != "" {
				commands = append(commands, trimmed)
			}
		}
	}

	return commands
}

// MatchesToolPattern reports whether a tool invocation matches a permission
// pattern. For Bash deny/ask checks, any matching subcommand is enough. Bash
// allow checks must use matchAllowPatterns, which requires every subcommand to
// be covered by the allow set.
func MatchesToolPattern(toolName string, args map[string]any, rule, pattern string) bool {
	if MatchRule(rule, pattern) {
		return true
	}

	if toolName != "Bash" {
		return false
	}

	cmd, ok := args["command"].(string)
	if !ok || cmd == "" {
		return false
	}

	for _, form := range bashRuleForms(cmd) {
		if MatchRule("Bash("+form+")", pattern) {
			return true
		}
	}

	return false
}

// MatchAllowList reports whether (toolName, args) matches the allow list.
// For Bash the contract is stricter than MatchesToolPattern: every parsed
// subcommand must independently be covered by at least one pattern. Returns
// the first matching pattern (or "" when no match).
//
// Use this for allow-rule evaluation. Use MatchesToolPattern for deny / ask
// where any-subcommand match is the right semantic.
func MatchAllowList(toolName string, args map[string]any, patterns []string) (string, bool) {
	if len(patterns) == 0 {
		return "", false
	}
	if toolName != "Bash" {
		rule := BuildRule(toolName, args)
		for _, pattern := range patterns {
			if MatchesToolPattern(toolName, args, rule, pattern) {
				return pattern, true
			}
		}
		return "", false
	}

	cmd, ok := args["command"].(string)
	if !ok || strings.TrimSpace(cmd) == "" {
		return "", false
	}
	subcommands := bashSubcommandRuleForms(cmd)
	if len(subcommands) == 0 {
		return "", false
	}

	var firstMatch string
	for _, forms := range subcommands {
		matched := ""
		for _, pattern := range patterns {
			for _, form := range forms {
				if MatchRule("Bash("+form+")", pattern) {
					matched = pattern
					break
				}
			}
			if matched != "" {
				break
			}
		}
		if matched == "" {
			return "", false
		}
		if firstMatch == "" {
			firstMatch = matched
		}
	}
	return firstMatch, true
}

// BypassImmuneReason returns a non-empty reason if the call would hit a
// bypass-immune safety check (sensitive file path, destructive bash command,
// or suspicious bash). Used by the main-loop gate (checkHardBlocks) and by
// the subagent gate, which collapses the resulting Ask into Deny.
func BypassImmuneReason(toolName string, args map[string]any) string {
	switch toolName {
	case "Edit", "Write", "NotebookEdit":
		if fp, ok := args["file_path"].(string); ok {
			if reason := isSensitivePath(fp); reason != "" {
				return reason
			}
		}
	case "Bash":
		if cmd, ok := args["command"].(string); ok {
			if isDestructiveCommand(cmd) {
				return "destructive command"
			}
			if reason := checkBashSecurity(cmd); reason != "" {
				return reason
			}
		}
	}
	return ""
}

// MatchRule checks if a rule matches a pattern.
// Rule format: "Tool(args)"
// Pattern format: "Tool(pattern)" where pattern supports:
//   - "*" matches any sequence of characters
//   - "**" matches any sequence including path separators
//   - "domain:" prefix for WebFetch domain matching
func MatchRule(rule, pattern string) bool {
	// Parse rule
	toolRule, argsRule := parseRule(rule)
	toolPat, argsPat := parseRule(pattern)

	// Tool names must match exactly
	if toolRule != toolPat {
		return false
	}

	// Match arguments using glob-like patterns
	return matchGlob(argsRule, argsPat)
}

// parseRule parses a rule string into tool name and arguments.
// "Bash(npm install)" -> ("Bash", "npm install")
func parseRule(s string) (tool, args string) {
	tool, args, found := strings.Cut(s, "(")
	if !found {
		return s, ""
	}
	return tool, strings.TrimSuffix(args, ")")
}

// matchGlob performs glob-like pattern matching.
// Supports:
//   - "*" matches any sequence of non-separator characters
//   - "**" matches any sequence including separators (path components)
//   - "?" matches a single character
//   - Exact string matching
func matchGlob(str, pattern string) bool {
	// Empty pattern matches empty string
	if pattern == "" {
		return str == ""
	}

	// Handle "**" pattern (matches everything)
	if pattern == "**" {
		return true
	}

	// Handle patterns with "**" (double star - matches any path)
	if strings.Contains(pattern, "**") {
		// Split on "**" and match each segment
		segments := strings.Split(pattern, "**")

		if len(segments) == 2 {
			prefix := segments[0]
			suffix := segments[1]

			// Remove leading/trailing slashes from segments for flexibility
			prefix = strings.TrimSuffix(prefix, "/")
			suffix = strings.TrimPrefix(suffix, "/")

			// Check prefix matches start
			if prefix != "" && !strings.HasPrefix(str, prefix) {
				return false
			}

			// Check suffix matches end (using simple glob for suffix like "*.go")
			if suffix != "" {
				// For suffix matching, we need to find if any suffix of the string matches the pattern
				// e.g., "*.go" should match "file.go" in "/path/to/file.go"
				// e.g., ".env.*" should match ".env.local" in "/path/to/.env.local"

				// Get the filename or last component if suffix looks like a filename pattern
				// For patterns like ".env.*" or "*.go", match against the basename
				if strings.Contains(suffix, "*") {
					// If the pattern looks like a filename (starts with . or contains .), try matching against the last path component
					// Get the last path component of the string
					lastSlash := strings.LastIndex(str, "/")
					var filename string
					if lastSlash >= 0 {
						filename = str[lastSlash+1:]
					} else {
						filename = str
					}

					// Try matching the filename against the suffix pattern
					if matchSimpleWildcard(filename, suffix) {
						return true
					}

					// Also try matching the whole remaining path (for patterns like "test/*.go")
					remaining := str
					if prefix != "" {
						remaining = strings.TrimPrefix(str, prefix)
						remaining = strings.TrimPrefix(remaining, "/")
					}
					return matchSimpleWildcard(remaining, suffix)
				}
				return strings.HasSuffix(str, suffix)
			}

			return true
		}
	}

	// Handle simple wildcard patterns
	if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
		return matchSimpleWildcard(str, pattern)
	}

	// Exact match
	return str == pattern
}

// matchSimpleWildcard matches a string against a pattern with * and ? wildcards.
// * matches any sequence of characters (including empty)
// ? matches exactly one character
func matchSimpleWildcard(str, pattern string) bool {
	// Use dynamic programming approach
	s, p := 0, 0
	starIdx, matchIdx := -1, 0

	for s < len(str) {
		if p < len(pattern) && (pattern[p] == '?' || pattern[p] == str[s]) {
			// Characters match or pattern has ?
			s++
			p++
		} else if p < len(pattern) && pattern[p] == '*' {
			// Found *, mark the position
			starIdx = p
			matchIdx = s
			p++
		} else if starIdx != -1 {
			// Mismatch after *, backtrack
			p = starIdx + 1
			matchIdx++
			s = matchIdx
		} else {
			// Hard mismatch
			return false
		}
	}

	// Check remaining pattern characters are all *
	for p < len(pattern) {
		if pattern[p] != '*' {
			return false
		}
		p++
	}

	return true
}
